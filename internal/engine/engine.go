// Package engine is the loaded text-to-speech pipeline shared by the CLI and
// the server: go-kokoro/text's pronunciation frontend and Kokoro model, which
// share one ONNX Runtime environment, loaded with kokoro-run's checks, timings
// and warm-up.
package engine

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/androiddrew/go-kokoro/text"
	"github.com/androiddrew/ortenv"
	ort "github.com/yalue/onnxruntime_go"
)

// SampleRate is Kokoro's output rate.
const SampleRate = text.SampleRate

// The pipeline, its requests and its audio are go-kokoro/text's.
type (
	Pipeline = text.Pipeline
	Frontend = text.Frontend
	Request  = text.Request
	Chunk    = text.Chunk
)

// ErrPronunciation is returned with Request.Strict for text the frontend
// can't fully pronounce.
var ErrPronunciation = text.ErrPronunciation

// Options are what loading a pipeline needs.
type Options struct {
	Frontend  g2p.Config    // ORTLibrary also selects the runtime for Kokoro
	Synthesis kokoro.Config // its ORTLibrary is ignored
	Fallback  string        // neural or espeak
	ESpeak    string        // eSpeak-ng executable, with fallback espeak
	// Controlled loads no frontend, for prepared Kokoro inputs only.
	Controlled bool
}

// NewFrontend loads the G2P engine and its fallback. The neural fallback's
// models are overridden together with the frontend's, from Frontend.ModelDir.
func NewFrontend(o Options) (*Frontend, error) {
	return text.NewFrontend(text.FrontendConfig{
		G2P:              o.Frontend,
		Fallback:         o.Fallback,
		FallbackModelDir: o.Frontend.ModelDir,
		ESpeak:           o.ESpeak,
	})
}

// Initialization records how long loading took.
type Initialization struct {
	kokoro.Initialization
	FrontendSeconds  float64 `json:"frontend_initialization_seconds"`
	SynthesisSeconds float64 `json:"synthesis_initialization_seconds"`
	TotalSeconds     float64 `json:"initialization_seconds"`
	RuntimeVersion   string  `json:"runtime_version"`
}

// Load initializes the runtime and loads the pipeline. The runtime stays loaded
// until process exit, so later pipelines reuse it. warmVoice and warmSpeed
// prepare one input up front, so a bad voice fails here rather than on the
// first request. With Controlled, the pipeline's Frontend is nil and it can't
// Stream.
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
