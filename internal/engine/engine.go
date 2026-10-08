// Package engine is the loaded text-to-speech pipeline shared by the CLI and
// the server: the pronunciation frontend with its fallback and the Kokoro model,
// which share one ONNX Runtime environment.
package engine

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode"

	g2p "github.com/androiddrew/go-g2p"
	"github.com/androiddrew/go-g2p/fallback"
	"github.com/androiddrew/go-g2p/fallback/espeak"
	"github.com/androiddrew/go-g2p/fallback/neural"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/androiddrew/go-ttsnorm/sentence"
	"github.com/androiddrew/ortenv"
	ort "github.com/yalue/onnxruntime_go"

	settings "github.com/androiddrew/kokoro-run/internal/config"
	"github.com/androiddrew/kokoro-run/internal/textprep"
)

// SampleRate is Kokoro's output rate.
const SampleRate = kokoro.SampleRate

// Options are what loading a pipeline needs.
type Options struct {
	Frontend  g2p.Config    // ORTLibrary also selects the runtime for Kokoro
	Synthesis kokoro.Config // its ORTLibrary is ignored
	Fallback  string        // neural or espeak
	ESpeak    string        // eSpeak-ng executable, with fallback espeak
	// Controlled loads no frontend, for prepared Kokoro inputs only.
	Controlled bool
}

// Frontend is a G2P engine together with the fallback it owns.
type Frontend struct {
	*g2p.Engine
	fallback fallback.Engine
}

// NewFrontend loads the G2P engine and its fallback.
func NewFrontend(o Options) (_ *Frontend, err error) {
	f := &Frontend{}
	defer func() {
		if err != nil {
			err = errors.Join(err, f.Close())
		}
	}()
	if o.Fallback == espeak.Name {
		f.fallback, err = espeak.New(espeak.Config{Executable: o.ESpeak})
	} else {
		f.fallback, err = neural.New(neural.Config{ModelDir: o.Frontend.ModelDir, ORTLibrary: o.Frontend.ORTLibrary})
	}
	if err != nil {
		return nil, err
	}
	fc := o.Frontend
	fc.Fallback = f.fallback
	f.Engine, err = g2p.New(fc)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Close shuts the engine before the fallback it uses.
func (f *Frontend) Close() error {
	var err error
	if f.Engine != nil {
		err = f.Engine.Close()
	}
	if f.fallback != nil {
		err = errors.Join(err, f.fallback.Close())
	}
	return err
}

// Initialization records how long loading took.
type Initialization struct {
	kokoro.Initialization
	FrontendSeconds  float64 `json:"frontend_initialization_seconds"`
	SynthesisSeconds float64 `json:"synthesis_initialization_seconds"`
	TotalSeconds     float64 `json:"initialization_seconds"`
	RuntimeVersion   string  `json:"runtime_version"`
}

// Pipeline is a loaded frontend and Kokoro model. Each of them serializes its
// own calls, so a pipeline serves one request at a time at full speed.
type Pipeline struct {
	Frontend  *Frontend // nil when loaded with Controlled
	Synthesis *kokoro.Engine
}

// Load initializes the runtime and loads the pipeline. The runtime stays loaded
// until process exit, so later pipelines reuse it. warmVoice and warmSpeed
// prepare one input up front, so a bad voice fails here rather than on the
// first request.
func Load(o Options, warmVoice string, warmSpeed float32) (_ *Pipeline, init Initialization, err error) {
	total := time.Now()
	defer func() { init.TotalSeconds = time.Since(total).Seconds() }()
	p := &Pipeline{}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Close())
		}
	}()
	if err = cudaDriverPresent(o.Synthesis.Provider); err != nil {
		return nil, init, err
	}
	start := time.Now()
	err = ortenv.Init(o.Frontend.ORTLibrary)
	runtimeSeconds := time.Since(start).Seconds()
	if err != nil {
		return nil, init, err
	}
	init.RuntimeVersion = ort.GetVersion()
	if !o.Controlled {
		start = time.Now()
		p.Frontend, err = NewFrontend(o)
		init.FrontendSeconds = time.Since(start).Seconds()
		if err != nil {
			return nil, init, err
		}
	}
	start = time.Now()
	sc := o.Synthesis
	sc.ORTLibrary = o.Frontend.ORTLibrary
	p.Synthesis, err = kokoro.New(sc)
	init.SynthesisSeconds = time.Since(start).Seconds()
	if err == nil {
		_, err = p.Synthesis.Prepare("həlˈO", warmVoice, warmSpeed)
	}
	if err != nil {
		return nil, init, err
	}
	init.Initialization = p.Synthesis.Initialization()
	init.RuntimeSeconds += runtimeSeconds
	return p, init, nil
}

// nvidiaDrivers are the NVIDIA GPU devices: the discrete driver's control
// device, and Jetson's integrated GPU memory device. One exists on Linux when
// the driver is loaded, and in a container only when it is given the GPU (the
// host's /proc/driver/nvidia shows through either way, so it can't be used).
var nvidiaDrivers = []string{"/dev/nvidiactl", "/dev/nvmap"}

// cudaDriverPresent fails for the CUDA provider on Linux without the NVIDIA
// driver. ONNX Runtime 1.22 crashes the process, rather than returning an
// error, when it enables CUDA without one.
func cudaDriverPresent(provider kokoro.Provider) error {
	if provider != kokoro.CUDA || runtime.GOOS != "linux" {
		return nil
	}
	for _, device := range nvidiaDrivers {
		if _, err := os.Stat(device); err == nil {
			return nil
		}
	}
	return fmt.Errorf("provider cuda: no NVIDIA GPU device is visible (%s); install the driver, or run the container with --gpus all (--runtime nvidia on Jetson)", strings.Join(nvidiaDrivers, ", "))
}

// Close releases the model and then the frontend. The ONNX Runtime environment
// stays loaded.
func (p *Pipeline) Close() error {
	var err error
	if p.Synthesis != nil {
		err = p.Synthesis.Close()
	}
	if p.Frontend != nil {
		err = errors.Join(err, p.Frontend.Close())
	}
	return err
}

// Request is one text to stream.
type Request struct {
	Text  string
	Voice string // a Kokoro voice; its prefix selects the dialect
	Speed float32
	Prep  textprep.Options
	Trim  bool          // trim each model run's leading and trailing silence
	Gap   time.Duration // silence before each sentence after the first
	// Strict fails on an unresolved or generation-limited pronunciation.
	// Otherwise it is spoken as far as known and reported in Chunk.Diagnostics.
	Strict bool
}

// Chunk is one model run's audio: a sentence, or part of a long one.
type Chunk struct {
	PCM         []float32 // mono at SampleRate, including any leading gap
	Text        string    // the sentence the audio is from
	Phonemes    string    // the phonemes this run spoke
	Tokens      int       // phoneme tokens the model ran on
	Diagnostics []g2p.Diagnostic
}

// ErrPronunciation is returned with Strict for text the frontend can't
// fully pronounce.
var ErrPronunciation = errors.New("incomplete pronunciation")

// unknown is the phoneme Misaki writes for a sound it couldn't resolve,
// which Kokoro can't speak.
const unknown = "❓"

// splitter keeps the first sentence short, so streamed audio starts soon.
var splitter = sentence.Splitter{}

// Stream prepares r's text, splits it into sentences and yields the audio of
// each model run as it is made. It stops at the first error, which it yields
// last. Cancelling ctx stops it between model runs.
func (p *Pipeline) Stream(ctx context.Context, r Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		dialect, ok := settings.VoiceLanguage(r.Voice)
		if !ok {
			yield(Chunk{}, fmt.Errorf("voice %q is not a US or UK English voice", r.Voice))
			return
		}
		gap := make([]float32, int(r.Gap.Seconds()*SampleRate))
		first := true
		for _, text := range splitter.Split(textprep.Text(r.Text, r.Prep)) {
			if err := ctx.Err(); err != nil {
				yield(Chunk{}, err)
				return
			}
			result, err := p.Frontend.Phonemize(ctx, g2p.Request{Text: text, Dialect: g2p.Dialect(dialect), AllowTruncated: !r.Strict})
			var incomplete *g2p.IncompleteError
			if errors.As(err, &incomplete) {
				if r.Strict {
					err = fmt.Errorf("%w: %s", ErrPronunciation, describe(result.Diagnostics))
				} else {
					// Speak what is known; the diagnostics say what isn't.
					result.Phonemes = strings.ReplaceAll(result.Phonemes, unknown, "")
					err = nil
				}
			}
			if err != nil {
				yield(Chunk{}, err)
				return
			}
			if !speakable(result.Phonemes) {
				continue
			}
			inputs, err := p.Synthesis.Prepare(result.Phonemes, r.Voice, r.Speed)
			if err != nil {
				yield(Chunk{}, err)
				return
			}
			for i, input := range inputs {
				audio, err := p.Synthesis.SynthesizePrepared(ctx, []kokoro.Chunk{input}, r.Trim)
				if err != nil {
					yield(Chunk{}, err)
					return
				}
				c := Chunk{PCM: audio.Samples, Text: text, Phonemes: input.Phonemes, Tokens: len(input.Tokens) - 2}
				if i == 0 {
					c.Diagnostics = result.Diagnostics
					if !first {
						c.PCM = append(gap[:len(gap):len(gap)], c.PCM...)
					}
				}
				first = false
				if !yield(c, nil) {
					return
				}
			}
		}
		if first {
			yield(Chunk{}, errors.New("the text has nothing to speak"))
		}
	}
}

// speakable reports whether phonemes have a sound to make, rather than only
// punctuation, which the model can't run on alone.
func speakable(phonemes string) bool {
	return strings.ContainsFunc(phonemes, func(r rune) bool { return !unicode.IsPunct(r) && !unicode.IsSpace(r) })
}

func describe(diagnostics []g2p.Diagnostic) string {
	parts := make([]string, len(diagnostics))
	for i, d := range diagnostics {
		parts[i] = fmt.Sprintf("%s %q", d.Code, d.Text)
	}
	return strings.Join(parts, ", ")
}
