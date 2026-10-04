package audio_test

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/hajimehoshi/go-mp3"

	"github.com/androiddrew/kokoro-run/internal/audio"
)

func TestWriteMP3(t *testing.T) {
	const rate = 24000
	cases := []struct {
		name    string
		samples int
	}{
		{"empty", 0},
		{"shorter than a frame", 100},
		{"a whole number of frames", 576 * 10},
		{"one and a half seconds of tone", rate * 3 / 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			samples := make([]float32, tc.samples)
			for i := range samples {
				samples[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/rate))
			}
			var buf bytes.Buffer
			if err := audio.WriteMP3(&buf, samples, rate); err != nil {
				t.Fatal(err)
			}
			// MPEG-2 layer III sync, 64 kbps, 24 kHz, mono.
			if b := buf.Bytes(); len(b) < 4 || b[0] != 0xff || b[1]&0xfe != 0xf2 || b[2]>>4 != 8 || (b[2]>>2)&3 != 1 || b[3]>>6 != 3 {
				t.Fatalf("first frame header % x, want MPEG-2 layer III, 64 kbps, 24 kHz, mono", b[:min(4, len(b))])
			}

			dec, err := mp3.NewDecoder(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if dec.SampleRate() != rate {
				t.Errorf("decoded sample rate %d, want %d", dec.SampleRate(), rate)
			}
			pcm, err := io.ReadAll(dec)
			if err != nil {
				t.Fatal(err)
			}
			// The decoder always writes 16-bit stereo. The encoder pads to whole
			// 576-sample frames and flushes its delay, so allow a few frames over.
			got := len(pcm) / 4
			if got < tc.samples || got > tc.samples+4*576 {
				t.Errorf("decoded %d samples, want %d to %d", got, tc.samples, tc.samples+4*576)
			}
		})
	}
}

func TestWriteMP3RejectsUnsupportedRate(t *testing.T) {
	if err := audio.WriteMP3(io.Discard, make([]float32, 10), 23456); err == nil {
		t.Error("WriteMP3 at 23456 Hz succeeded, want an error")
	}
}

func TestMP3EncoderChunkingDoesNotChangeTheOutput(t *testing.T) {
	const rate = 24000
	samples := make([]float32, rate)
	for i := range samples {
		samples[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/rate))
	}
	var whole bytes.Buffer
	if err := audio.WriteMP3(&whole, samples, rate); err != nil {
		t.Fatal(err)
	}

	var chunked bytes.Buffer
	enc, err := audio.NewMP3Encoder(&chunked, rate)
	if err != nil {
		t.Fatal(err)
	}
	rest := samples
	for _, n := range []int{100, 1000, 576, 7, 5000} {
		if err := enc.Write(rest[:n]); err != nil {
			t.Fatal(err)
		}
		rest = rest[n:]
	}
	if err := enc.Write(rest); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chunked.Bytes(), whole.Bytes()) {
		t.Errorf("chunked encoding is %d bytes and differs from the %d-byte whole encoding", chunked.Len(), whole.Len())
	}
}
