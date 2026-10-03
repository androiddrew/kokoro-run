package audio_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/androiddrew/kokoro-run/internal/audio"
)

func TestPCM16(t *testing.T) {
	// 0, 32767, -32767, 16384, 32767 (clipped), -32767 (clipped), 8192; little-endian
	want, _ := hex.DecodeString("0000ff7f018000" + "40ff7f01800020")
	got := audio.PCM16([]float32{0, 1, -1, 0.5, 2, -2, 0.25})
	if !bytes.Equal(got, want) {
		t.Errorf("got  %x\nwant %x", got, want)
	}
	if got := audio.PCM16(nil); len(got) != 0 {
		t.Errorf("PCM16(nil) = %x, want empty", got)
	}
}
