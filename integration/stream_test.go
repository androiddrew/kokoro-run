package integration_test

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"

	"github.com/androiddrew/kokoro-run/internal/engine"
	"github.com/androiddrew/kokoro-run/internal/textprep"
)

func loadPipeline(t *testing.T) *engine.Pipeline {
	t.Helper()
	assets, library := os.Getenv("KOKORO_TEST_ASSETS"), os.Getenv("KOKORO_TEST_ORT")
	if assets == "" || library == "" {
		t.Skip("set KOKORO_TEST_ASSETS and KOKORO_TEST_ORT for streaming synthesis")
	}
	p, _, err := engine.Load(engine.Options{
		Frontend:  g2p.Config{ORTLibrary: library},
		Synthesis: kokoro.Config{ModelPath: filepath.Join(assets, "kokoro-v1.0.onnx"), VoicesPath: filepath.Join(assets, "voices-v1.0.bin")},
		Fallback:  "neural",
	}, "af_heart", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func TestStream(t *testing.T) {
	p := loadPipeline(t)
	ctx := context.Background()
	const gap = 100 * time.Millisecond
	req := engine.Request{
		Text:  "**Hello** there. The meeting is at 3:05 p.m. on May 5, 2026! How are you today?",
		Voice: "af_heart", Speed: 1, Trim: true, Gap: gap,
		Prep: textprep.Options{Markdown: true, Normalize: true},
	}
	var chunks []engine.Chunk
	for c, err := range p.Stream(ctx, req) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, c)
	}
	var texts []string
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want one per sentence: %q", len(chunks), texts)
	}
	if !strings.Contains(chunks[1].Text, "three oh five PM") || len(chunks[0].Diagnostics) != 0 {
		t.Errorf("sentence 2 not normalized: %q", chunks[1].Text)
	}
	gapSamples := int(gap.Seconds() * engine.SampleRate)
	for i, c := range chunks {
		if len(c.PCM) <= gapSamples || c.Tokens == 0 {
			t.Fatalf("chunk %d: %d samples, %d tokens", i, len(c.PCM), c.Tokens)
		}
		leading := 0
		for _, s := range c.PCM {
			if s != 0 {
				break
			}
			leading++
		}
		if (i == 0 && leading >= gapSamples) || (i > 0 && leading < gapSamples) {
			t.Errorf("chunk %d starts with %d zero samples; the gap is %d after the first", i, leading, gapSamples)
		}
	}

	// Stopping early leaves the pipeline usable.
	for range p.Stream(ctx, req) {
		break
	}
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	for _, err := range p.Stream(ctx2, req) {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled stream: %v", err)
		}
	}
	for _, err := range p.Stream(ctx, engine.Request{Text: "...", Voice: "af_heart", Speed: 1}) {
		if err == nil {
			t.Fatal("punctuation alone produced audio")
		}
	}
}

func TestStreamPronunciation(t *testing.T) {
	p := loadPipeline(t)
	// Past the neural fallback's 62-code-point limit, so its output is truncated.
	long := "Say " + strings.Repeat("zorblax", 10) + " now."
	req := engine.Request{Text: long, Voice: "bf_emma", Speed: 1, Trim: true}
	var diagnostics int
	for c, err := range p.Stream(context.Background(), req) {
		if err != nil {
			t.Fatalf("lenient: %v", err)
		}
		diagnostics += len(c.Diagnostics)
	}
	if diagnostics == 0 {
		t.Fatal("lenient stream reported no diagnostics for a truncated word")
	}
	req.Strict = true
	for _, err := range p.Stream(context.Background(), req) {
		if !errors.Is(err, engine.ErrPronunciation) {
			t.Fatalf("strict: got %v, want ErrPronunciation", err)
		}
	}
}

// TestSentencePause measures the silence Kokoro leaves between two
// sentences synthesized as one input, the pause per-sentence streaming
// removes when it trims each sentence. It informs server.chunk_gap.
func TestSentencePause(t *testing.T) {
	p := loadPipeline(t)
	var pauses []float64
	for _, text := range []string{"Hello there. How are you today?", "It is late. We should go home now.", "Yes! That is right."} {
		r, err := p.Frontend.Phonemize(context.Background(), g2p.Request{Text: text, Dialect: g2p.US})
		if err != nil {
			t.Fatal(err)
		}
		audio, err := p.Synthesis.Synthesize(context.Background(), kokoro.Request{Phonemes: r.Phonemes, Voice: "af_heart", Speed: 1, Trim: true})
		if err != nil {
			t.Fatal(err)
		}
		pauses = append(pauses, longestQuiet(audio.Samples))
	}
	t.Logf("longest inner pause per two-sentence input: %.0f ms, %.0f ms, %.0f ms", pauses[0]*1000, pauses[1]*1000, pauses[2]*1000)
}

// longestQuiet is the longest run, in seconds, of 10 ms windows more than
// 40 dB below the peak, excluding the ends.
func longestQuiet(samples []float32) float64 {
	var peak float64
	for _, s := range samples {
		peak = math.Max(peak, math.Abs(float64(s)))
	}
	window := engine.SampleRate / 100
	threshold := peak * math.Pow(10, -40.0/20)
	run, longest := 0, 0
	for i := 0; i+window <= len(samples); i += window {
		var rms float64
		for _, s := range samples[i : i+window] {
			rms += float64(s) * float64(s)
		}
		if math.Sqrt(rms/float64(window)) < threshold {
			run++
		} else {
			longest, run = max(longest, run), 0
		}
	}
	return float64(longest) / 100
}
