package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	g2p "github.com/androiddrew/go-g2p"
	"github.com/androiddrew/go-g2p/fallback/neural"
	kokoro "github.com/androiddrew/go-kokoro"
	ort "github.com/yalue/onnxruntime_go"
)

// Exercise both destruction orders. The surviving engine must remain usable,
// including after a partially initialized frontend fails with Kokoro still live.
func TestSharedFrontendRuntime(t *testing.T) {
	assets, library := os.Getenv("KOKORO_TEST_ASSETS"), os.Getenv("KOKORO_TEST_ORT")
	c := g2p.Config{ORTLibrary: library}
	if assets == "" || library == "" {
		t.Skip("set KOKORO_TEST_ASSETS and KOKORO_TEST_ORT for combined inference")
	}
	for _, frontendFirst := range []bool{true, false} {
		fallback, err := neural.New(neural.Config{ORTLibrary: library})
		if err != nil {
			t.Fatal(err)
		}
		c.Fallback = fallback
		front, err := g2p.New(c)
		if err != nil {
			fallback.Close()
			t.Fatal(err)
		}
		synth, err := kokoro.New(kokoro.Config{ORTLibrary: library, ModelPath: filepath.Join(assets, "kokoro-v1.0.onnx"), VoicesPath: filepath.Join(assets, "voices-v1.0.bin")})
		if err != nil {
			front.Close()
			fallback.Close()
			t.Fatal(err)
		}
		bad := c
		bad.ModelDir = t.TempDir()
		if _, err = g2p.New(bad); err == nil {
			t.Fatal("accepted missing POS model")
		}
		r, err := front.Phonemize(context.Background(), g2p.Request{Text: "Hello, outofdictionary world!", Dialect: "us"})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.FallbackCalls) != 1 {
			t.Fatalf("fallback not exercised: %+v", r)
		}
		if frontendFirst {
			if err = front.Close(); err != nil {
				t.Fatal(err)
			}
		}
		audio, err := synth.Synthesize(context.Background(), kokoro.Request{Phonemes: r.Phonemes, Voice: "af_heart", Speed: 1, Trim: true})
		if err != nil || len(audio.Samples) == 0 {
			t.Fatalf("synthesis: %v", err)
		}
		if err = synth.Close(); err != nil {
			t.Fatal(err)
		}
		if !frontendFirst {
			if _, err = front.Phonemize(context.Background(), g2p.Request{Text: "outofdictionary", Dialect: "gb"}); err != nil {
				t.Fatal(err)
			}
			if err = front.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err = fallback.Close(); err != nil {
			t.Fatal(err)
		}
		// ortenv v0.1.0 unloaded the runtime here and reloaded it on the next
		// iteration, which crashes CUDA providers (androiddrew/ortenv#2).
		if !ort.IsInitialized() {
			t.Fatal("closing every engine destroyed the environment")
		}
	}
}
