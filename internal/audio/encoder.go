package audio

import (
	"encoding/binary"
	"io"
)

// Encoder encodes mono float32 PCM one chunk at a time. Each Write produces
// at most one Write on the underlying writer, so a writer that flushes on
// every Write sends audio as soon as it is encoded. Close writes whatever
// the encoder still holds.
type Encoder interface {
	Write(samples []float32) error
	Close() error
}

// NewPCMEncoder returns an Encoder writing samples as by PCM16.
func NewPCMEncoder(w io.Writer) Encoder { return pcmEncoder{w} }

type pcmEncoder struct{ w io.Writer }

func (e pcmEncoder) Write(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	_, err := e.w.Write(PCM16(samples))
	return err
}

func (pcmEncoder) Close() error { return nil }

// NewWAVStreamEncoder returns an Encoder writing a WAV whose length isn't
// known up front: the RIFF and data sizes are 0xFFFFFFFF. The header goes
// out with the first samples, or on Close if there are none.
func NewWAVStreamEncoder(w io.Writer, sampleRate int) Encoder {
	return &wavStreamEncoder{w: w, header: wavHeader(0xFFFFFFFF, 0xFFFFFFFF, sampleRate, 0)}
}

type wavStreamEncoder struct {
	w      io.Writer
	header []byte // nil once written
}

func (e *wavStreamEncoder) Write(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	b := appendPCM16(e.header, samples)
	e.header = nil
	_, err := e.w.Write(b)
	return err
}

func (e *wavStreamEncoder) Close() error {
	if e.header == nil {
		return nil
	}
	_, err := e.w.Write(e.header)
	e.header = nil
	return err
}

// wavHeader is the 44-byte header of a mono 16-bit PCM WAV, with capacity
// for the given number of samples after it.
func wavHeader(riffLen, dataLen uint32, sampleRate, samples int) []byte {
	le := binary.LittleEndian
	b := make([]byte, 0, 44+2*samples)
	b = append(b, "RIFF"...)
	b = le.AppendUint32(b, riffLen)
	b = append(b, "WAVE"...)
	b = append(b, "fmt "...)
	b = le.AppendUint32(b, 16) // fmt chunk size
	b = le.AppendUint16(b, 1)  // PCM
	b = le.AppendUint16(b, 1)  // mono
	b = le.AppendUint32(b, uint32(sampleRate))
	b = le.AppendUint32(b, uint32(sampleRate*2)) // byte rate
	b = le.AppendUint16(b, 2)                    // block align
	b = le.AppendUint16(b, 16)                   // bits per sample
	b = append(b, "data"...)
	b = le.AppendUint32(b, dataLen)
	return b
}
