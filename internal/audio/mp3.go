package audio

import (
	"bytes"
	"io"

	"github.com/braheezy/shine-mp3/pkg/mp3"
)

// mp3Bitrate is the mp3 bitrate in kbps.
const mp3Bitrate = 64

// mp3Flush is the silence appended so the encoder's analysis delay doesn't
// cut off the end of the audio.
const mp3Flush = 2 * 576

// WriteMP3 writes mono samples as 64 kbps MPEG layer III, with samples
// converted as by PCM16. Empty input still writes a valid, silent mp3.
func WriteMP3(w io.Writer, samples []float32, sampleRate int) error {
	enc, err := NewMP3Encoder(w, sampleRate)
	if err != nil {
		return err
	}
	if err := enc.Write(samples); err != nil {
		return err
	}
	return enc.Close()
}

// NewMP3Encoder returns an Encoder writing 64 kbps MPEG layer III, as
// WriteMP3 does. It encodes whole frames as they fill and holds the rest, so
// how the samples are split across Writes doesn't change the output.
func NewMP3Encoder(w io.Writer, sampleRate int) (Encoder, error) {
	version, err := mp3.CheckConfig(sampleRate, mp3Bitrate)
	if err != nil {
		return nil, err
	}
	enc := mp3.NewEncoder(sampleRate, 1)
	// NewEncoder always picks 128 kbps; redo its bitrate-derived fields.
	enc.Mpeg.Bitrate = mp3Bitrate
	enc.Mpeg.BitrateIndex = 8 // 64 kbps in the MPEG-2 and 2.5 tables
	if version == mp3.MPEG_I {
		enc.Mpeg.BitrateIndex = 5 // 64 kbps in the MPEG-1 table
	}
	frame := int(enc.Mpeg.GranulesPerFrame * mp3.GRANULE_SIZE)
	slots := float64(frame) / float64(sampleRate) * mp3Bitrate * 1000 / float64(enc.Mpeg.BitsPerSlot)
	enc.Mpeg.WholeSlotsPerFrame = int64(slots)
	enc.Mpeg.FracSlotsPerFrame = slots - float64(enc.Mpeg.WholeSlotsPerFrame)
	enc.Mpeg.SlotLag = -enc.Mpeg.FracSlotsPerFrame
	return &mp3Encoder{w: w, enc: enc, frame: frame}, nil
}

type mp3Encoder struct {
	w       io.Writer
	enc     *mp3.Encoder
	frame   int     // samples per frame
	pending []int16 // less than a frame
	buf     bytes.Buffer
}

func (e *mp3Encoder) Write(samples []float32) error {
	for _, s := range samples {
		e.pending = append(e.pending, int16Sample(s))
	}
	whole := len(e.pending) / e.frame * e.frame
	if whole == 0 {
		return nil
	}
	err := e.encode(e.pending[:whole])
	e.pending = append(e.pending[:0], e.pending[whole:]...)
	return err
}

// Close encodes the held samples and the flush silence; shine pads the last
// frame with silence.
func (e *mp3Encoder) Close() error {
	err := e.encode(append(e.pending, make([]int16, mp3Flush)...))
	e.pending = nil
	return err
}

func (e *mp3Encoder) encode(pcm []int16) error {
	e.buf.Reset()
	if err := e.enc.Write(&e.buf, pcm); err != nil {
		return err
	}
	_, err := e.w.Write(e.buf.Bytes())
	return err
}
