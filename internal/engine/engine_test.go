package engine

import (
	"os"
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
	saved := nvidiaDrivers
	defer func() { nvidiaDrivers = saved }()
	dir := t.TempDir()
	nvidiaDrivers = []string{filepath.Join(dir, "nvidiactl"), filepath.Join(dir, "nvmap")}
	if err := cudaDriverPresent(kokoro.CUDA); err == nil || !strings.Contains(err.Error(), "--gpus all") {
		t.Fatalf("missing driver: %v", err)
	}
	if err := cudaDriverPresent(kokoro.CPU); err != nil {
		t.Fatalf("cpu: %v", err)
	}
	// Jetson has only the second device.
	if err := os.WriteFile(nvidiaDrivers[1], nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cudaDriverPresent(kokoro.CUDA); err != nil {
		t.Fatalf("jetson device: %v", err)
	}
	os.Remove(nvidiaDrivers[1])
	if _, _, err := Load(Options{Synthesis: kokoro.Config{Provider: kokoro.CUDA}}, "af_heart", 1); err == nil || !strings.Contains(err.Error(), "NVIDIA GPU device") {
		t.Fatalf("Load without a driver: %v", err)
	}
}
