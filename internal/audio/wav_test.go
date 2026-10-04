package audio_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/androiddrew/kokoro-run/internal/audio"
)

func TestWriteWAV(t *testing.T) {
	cases := []struct {
		name    string
		samples []float32
		// little-endian hex, spaces for readability
		want string
	}{
		{
			name:    "empty",
			samples: nil,
			want: "52494646 24000000 57415645" + // "RIFF", 36, "WAVE"
				" 666d7420 10000000 0100 0100" + // "fmt ", 16, PCM, mono
				" c05d0000 80bb0000 0200 1000" + // 24000 Hz, 48000 bytes/s, align 2, 16 bits
				" 64617461 00000000", // "data", 0 bytes
		},
		{
			name:    "samples are scaled by 32767, rounded and clipped",
			samples: []float32{0, 1, -1, 0.5, 2, -2, 0.25},
			want: "52494646 32000000 57415645" + // 36 + 14
				" 666d7420 10000000 0100 0100" +
				" c05d0000 80bb0000 0200 1000" +
				" 64617461 0e000000" + // 14 bytes
				" 0000 ff7f 0180 0040 ff7f 0180 0020", // 0, 32767, -32767, 16384, 32767, -32767, 8192
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := hex.DecodeString(strings.ReplaceAll(c.want, " ", ""))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := audio.WriteWAV(&buf, c.samples, 24000); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("got\n%s\nwant\n%s", hex.Dump(buf.Bytes()), hex.Dump(want))
			}
		})
	}
}

func TestWAVStreamEncoder(t *testing.T) {
	header := "52494646 ffffffff 57415645" + // "RIFF", unknown size, "WAVE"
		" 666d7420 10000000 0100 0100" + // "fmt ", 16, PCM, mono
		" c05d0000 80bb0000 0200 1000" + // 24000 Hz, 48000 bytes/s, align 2, 16 bits
		" 64617461 ffffffff" // "data", unknown size
	cases := []struct {
		name   string
		chunks [][]float32
		// each Write on the underlying writer, little-endian hex
		want []string
	}{
		{"no audio is just the header", nil, []string{header}},
		{"the header goes out with the first chunk", [][]float32{{0, 0.5}, {-0.5}}, []string{header + " 0000 0040", "00c0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var w writeRecorder
			enc := audio.NewWAVStreamEncoder(&w, 24000)
			for _, chunk := range c.chunks {
				if err := enc.Write(chunk); err != nil {
					t.Fatal(err)
				}
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			if len(w.writes) != len(c.want) {
				t.Fatalf("%d writes, want %d: %x", len(w.writes), len(c.want), w.writes)
			}
			for i, want := range c.want {
				if got := hex.EncodeToString(w.writes[i]); got != strings.ReplaceAll(want, " ", "") {
					t.Errorf("write %d\n got %s\nwant %s", i, got, strings.ReplaceAll(want, " ", ""))
				}
			}
		})
	}
}

func TestPCMEncoder(t *testing.T) {
	var w writeRecorder
	enc := audio.NewPCMEncoder(&w)
	for _, chunk := range [][]float32{{0, 0.5}, {-0.5}} {
		if err := enc.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", w.writes); got != "[00000040 00c0]" {
		t.Errorf("writes %s, want [00000040 00c0], one per chunk", got)
	}
}

// writeRecorder records each Write separately.
type writeRecorder struct{ writes [][]byte }

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.writes = append(w.writes, bytes.Clone(p))
	return len(p), nil
}
