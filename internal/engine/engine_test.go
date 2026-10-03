package engine

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	kokoro "github.com/androiddrew/go-kokoro"
)

func TestCUDADriverCheck(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the driver check is Linux-only")
	}
	saved := nvidiaDriver
	defer func() { nvidiaDriver = saved }()
	nvidiaDriver = filepath.Join(t.TempDir(), "missing")
	if err := cudaDriverPresent(kokoro.CUDA); err == nil || !strings.Contains(err.Error(), "--gpus all") {
		t.Fatalf("missing driver: %v", err)
	}
	if err := cudaDriverPresent(kokoro.CPU); err != nil {
		t.Fatalf("cpu: %v", err)
	}
	if _, _, err := Load(Options{Synthesis: kokoro.Config{Provider: kokoro.CUDA}}, "af_heart", 1); err == nil || !strings.Contains(err.Error(), "NVIDIA driver") {
		t.Fatalf("Load without a driver: %v", err)
	}
}
