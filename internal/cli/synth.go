package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/spf13/cobra"

	"github.com/androiddrew/kokoro-run/internal/engine"
)

type audioStats struct {
	SampleRate       int     `json:"sample_rate"`
	Samples          int     `json:"samples"`
	Seconds          float64 `json:"seconds"`
	Peak             float64 `json:"peak"`
	OutsideFullScale int     `json:"outside_full_scale"`
	Chunks           int     `json:"chunks"`
}

type measurement struct {
	kokoro.Timings
	FrontendSeconds   float64        `json:"preprocessing_seconds"`
	SynthesisSeconds  float64        `json:"synthesis_seconds"`
	WAVSeconds        float64        `json:"wav_seconds"`
	GenerationSeconds float64        `json:"generation_seconds"`
	RTF               float64        `json:"rtf"`
	Audio             audioStats     `json:"audio"`
	PreparedText      string         `json:"prepared_text,omitempty"`
	Frontend          g2p.Result     `json:"frontend"`
	Output            string         `json:"output"`
	Inputs            []kokoro.Chunk `json:"inputs"`
}

func statistics(result kokoro.Result) (audioStats, error) {
	s := audioStats{SampleRate: result.SampleRate, Samples: len(result.Samples), Seconds: float64(len(result.Samples)) / float64(result.SampleRate), Chunks: len(result.Chunks)}
	for _, sample := range result.Samples {
		v := math.Abs(float64(sample))
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return s, errors.New("non-finite audio")
		}
		s.Peak = math.Max(s.Peak, v)
		if v > 1 {
			s.OutsideFullScale++
		}
	}
	if s.SampleRate != 24000 || s.Samples == 0 || s.Peak == 0 {
		return s, errors.New("empty, silent or invalid-rate audio")
	}
	return s, nil
}

func generate(p *engine.Pipeline, cmd *cobra.Command, c config, text, output string) (m measurement, err error) {
	return generateInput(p, cmd, c, text, output, nil)
}

func generateInput(p *engine.Pipeline, cmd *cobra.Command, c config, text, output string, prepared []kokoro.Chunk) (m measurement, err error) {
	start := time.Now()
	if prepared == nil {
		m.PreparedText = c.prepare(text)
		m.Frontend, err = p.Frontend.Phonemize(cmd.Context(), g2p.Request{Text: m.PreparedText, Dialect: g2p.Dialect(c.Language)})
		m.FrontendSeconds = time.Since(start).Seconds()
		if dErr := diagnostics(cmd.ErrOrStderr(), m.Frontend); err != nil || dErr != nil {
			return m, errors.Join(err, dErr)
		}
	}
	inferenceStart := time.Now()
	var result kokoro.Result
	if prepared == nil {
		result, err = p.Synthesis.Synthesize(cmd.Context(), kokoro.Request{Phonemes: m.Frontend.Phonemes, Voice: c.Voice, Speed: c.Speed, Trim: c.Trim})
	} else {
		result, err = p.Synthesis.SynthesizePrepared(cmd.Context(), prepared, c.Trim)
	}
	m.SynthesisSeconds = time.Since(inferenceStart).Seconds()
	m.Timings = result.Timings
	if err != nil {
		return m, err
	}
	postStart := time.Now()
	m.Audio, err = statistics(result)
	for _, chunk := range result.Chunks {
		m.Inputs = append(m.Inputs, chunk.Chunk)
	}
	m.PostprocessingSeconds += time.Since(postStart).Seconds()
	if err != nil {
		return m, err
	}
	if err = cmd.Context().Err(); err != nil {
		return m, err
	}
	wavStart := time.Now()
	err = writeNew(output, func(w io.Writer) error { return kokoro.WriteWAV(w, result.Samples) })
	m.WAVSeconds = time.Since(wavStart).Seconds()
	m.GenerationSeconds = time.Since(start).Seconds()
	m.RTF = m.GenerationSeconds / m.Audio.Seconds
	m.Output = output
	if err == nil && m.Audio.OutsideFullScale > 0 {
		_, err = fmt.Fprintf(cmd.ErrOrStderr(), "audio: %d samples exceed full scale (peak %.6f); PCM16 output saturates\n", m.Audio.OutsideFullScale, m.Audio.Peak)
	}
	return m, err
}

func synthCommand() *cobra.Command {
	var c config
	var input inputFlags
	var output, report string
	cmd := &cobra.Command{Use: "synth", Short: "Synthesize text to a new 24 kHz mono PCM16 WAV", Args: cobra.NoArgs}
	frontendFlags(cmd)
	synthesisFlags(cmd)
	languageFlag(cmd)
	textPrepFlags(cmd)
	textFlags(cmd, &input)
	cmd.Flags().StringVarP(&output, "output", "o", "", "New WAV path (required; existing files are preserved)")
	cmd.Flags().StringVar(&report, "report", "", "Optional new JSON diagnostics/timing report path")
	_ = cmd.MarkFlagRequired("output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		if c, err = resolve(cmd, true); err != nil {
			return err
		}
		if output == "" || output == "-" {
			return errors.New("--output must be a new WAV file path")
		}
		if report != "" && filepath.Clean(report) == filepath.Clean(output) {
			return errors.New("--report and --output must be different files")
		}
		text, err := readText(cmd, input)
		if err != nil {
			return err
		}
		p, init, err := engine.Load(c.options(false), c.Voice, c.Speed)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, p.Close()) }()
		m, err := generate(p, cmd, c, text, output)
		if err != nil {
			return err
		}
		if report != "" {
			err = writeNew(report, func(w io.Writer) error {
				return encode(w, struct {
					Build          map[string]string     `json:"build"`
					Config         config                `json:"config"`
					Initialization engine.Initialization `json:"initialization"`
					Measurement    measurement           `json:"measurement"`
				}{buildMetadata(), c, init, m})
			})
			if err != nil {
				return fmt.Errorf("WAV saved to %s; report failed: %w", output, err)
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %.3fs audio, %d chunks, %s/%s\n", output, m.Audio.Seconds, m.Audio.Chunks, c.Fallback, c.Synthesis.Provider)
		return err
	}
	return cmd
}

func benchCommand() *cobra.Command {
	var c config
	var input inputFlags
	var output, controlled string
	var warmup, repeat int
	cmd := &cobra.Command{Use: "bench", Short: "Measure repeated text or controlled-tensor generation (JSONL)", Args: cobra.NoArgs,
		Long: "Measure first, warm-up and warm requests using one loaded pipeline.\nUse --controlled for shared precomputed tensors instead of text.\nIncludes completed output retrieval and WAV close. Downloads are never timed."}
	frontendFlags(cmd)
	synthesisFlags(cmd)
	languageFlag(cmd)
	textPrepFlags(cmd)
	textFlags(cmd, &input)
	cmd.Flags().StringVarP(&output, "output", "o", "", "New directory for WAVs and measurements.jsonl (required)")
	cmd.Flags().StringVar(&controlled, "controlled", "", "JSON array of precomputed Kokoro chunks; bypass frontend")
	cmd.MarkFlagsMutuallyExclusive("controlled", "text")
	cmd.MarkFlagsMutuallyExclusive("controlled", "file")
	cmd.Flags().IntVar(&warmup, "warmup", 3, "Warm-up requests after the separately recorded first request")
	cmd.Flags().IntVar(&repeat, "repeat", 30, "Measured warm requests (0 with --warmup 0 records only first)")
	_ = cmd.MarkFlagRequired("output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		if c, err = resolve(cmd, true); err != nil {
			return err
		}
		if warmup < 0 || warmup > 10000 || repeat < 0 || repeat > 10000 || (repeat == 0 && warmup != 0) {
			return errors.New("--warmup and --repeat must be 0..10000; --repeat 0 requires --warmup 0")
		}
		if output == "" {
			return errors.New("--output directory is required")
		}
		var text string
		var prepared []kokoro.Chunk
		if controlled == "" {
			text, err = readText(cmd, input)
		} else {
			var data []byte
			data, err = os.ReadFile(controlled)
			if err == nil {
				err = json.Unmarshal(data, &prepared)
			}
			if err == nil && len(prepared) == 0 {
				err = errors.New("controlled input has no chunks")
			}
		}
		if err != nil {
			return err
		}
		if err = os.Mkdir(output, 0755); err != nil {
			return err
		}
		p, init, err := engine.Load(c.options(controlled != ""), c.Voice, c.Speed)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, p.Close()) }()
		f, err := os.OpenFile(filepath.Join(output, "measurements.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, f.Close()) }()
		enc := json.NewEncoder(f)
		if err = enc.Encode(map[string]any{"type": "metadata", "schema": 2, "build": buildMetadata(), "config": c, "controlled": controlled, "initialization": init, "warmup": warmup, "repeat": repeat, "cache": "one process; filesystem cache uncontrolled", "timing": "separate runtime/frontend/voice/vocabulary/model stages; model includes inspection and inference sessions; inference includes synchronous native runs, output copies and finite checks; generation includes WAV write/close; no fsync"}); err != nil {
			return err
		}
		for i := 0; i < 1+warmup+repeat; i++ {
			if err = cmd.Context().Err(); err != nil {
				return err
			}
			phase := "warm"
			if i == 0 {
				phase = "first"
			} else if i <= warmup {
				phase = "warmup"
			}
			m, e := generateInput(p, cmd, c, text, filepath.Join(output, fmt.Sprintf("%04d-%s.wav", i, phase)), prepared)
			if e != nil {
				return e
			}
			if err = enc.Encode(map[string]any{"type": "measurement", "phase": phase, "index": i, "measurement": m}); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(output, "measurements.jsonl"))
		return err
	}
	return cmd
}
