package audio

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// ffmpegOutputs are the ffmpeg output arguments for each format
// NewFFmpegEncoder produces.
var ffmpegOutputs = map[string][]string{
	"opus": {"-c:a", "libopus", "-f", "ogg"},
	"aac":  {"-c:a", "aac", "-f", "adts"},
	"flac": {"-c:a", "flac", "-f", "flac"},
}

// NewFFmpegEncoder starts the ffmpeg binary at path to encode mono samples
// as format (opus in Ogg, aac in ADTS, or flac). Samples go to ffmpeg's
// stdin as by PCM16, and its stdout is copied to w as it arrives, so w's
// Writes don't line up with the encoder's. Cancelling ctx kills ffmpeg.
// Close must be called to reap the process.
func NewFFmpegEncoder(ctx context.Context, w io.Writer, path string, sampleRate int, format string) (Encoder, error) {
	out, ok := ffmpegOutputs[format]
	if !ok {
		return nil, fmt.Errorf("ffmpeg: unsupported format %q", format)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "1", "-i", "-"}
	cmd := exec.CommandContext(ctx, path, append(append(args, out...), "-")...)
	e := &ffmpegEncoder{cmd: cmd, format: format}
	cmd.Stdout = killOnError{w, cmd}
	cmd.Stderr = &e.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	e.stdin = stdin
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg %s: %w", format, err)
	}
	return e, nil
}

// killOnError kills ffmpeg when its output can't be written, so it can't
// block on a full stdout pipe while Write blocks on its stdin.
type killOnError struct {
	w   io.Writer
	cmd *exec.Cmd
}

func (k killOnError) Write(p []byte) (int, error) {
	n, err := k.w.Write(p)
	if err != nil {
		k.cmd.Process.Kill()
	}
	return n, err
}

type ffmpegEncoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr strings.Builder
	format string
}

func (e *ffmpegEncoder) Write(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	if _, err := e.stdin.Write(PCM16(samples)); err != nil {
		return fmt.Errorf("ffmpeg %s: %w", e.format, err)
	}
	return nil
}

// Close ends ffmpeg's input and waits for it to write the rest of its output.
func (e *ffmpegEncoder) Close() error {
	e.stdin.Close()
	if err := e.cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", e.format, err, strings.TrimSpace(e.stderr.String()))
	}
	return nil
}
