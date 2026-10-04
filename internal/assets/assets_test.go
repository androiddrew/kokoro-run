package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	files, err := Files()
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", files, err)
	}
	for _, f := range files {
		if f.Path == "" || len(f.SHA256) != 64 || f.Bytes <= 0 || !strings.HasPrefix(f.URL, "https://") {
			t.Errorf("incomplete entry %+v", f)
		}
	}
}

func TestPull(t *testing.T) {
	content := []byte("model bytes")
	sum := sha256.Sum256(content)
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		if r.URL.Path == "/bad" {
			w.Write([]byte("model bytez"))
			return
		}
		w.Write(content)
	}))
	defer srv.Close()
	saved := manifest
	defer func() { manifest = saved }()
	manifest = []byte(`{"files":[{"path":"kokoro/m.onnx","sha256":"` + hex.EncodeToString(sum[:]) + `","bytes":11,"url":"` + srv.URL + `/m"}]}`)

	dir := t.TempDir()
	var log bytes.Buffer
	if err := Pull(context.Background(), srv.Client(), dir, &log); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "kokoro/m.onnx")); !bytes.Equal(got, content) || served != 1 {
		t.Fatalf("pulled %q after %d requests", got, served)
	}
	if err := Pull(context.Background(), srv.Client(), dir, &log); err != nil || served != 1 {
		t.Fatalf("verified file downloaded again: %v, %d requests", err, served)
	}

	// A corrupt download is rejected and leaves nothing behind.
	manifest = []byte(`{"files":[{"path":"kokoro/x.onnx","sha256":"` + hex.EncodeToString(sum[:]) + `","bytes":11,"url":"` + srv.URL + `/bad"}]}`)
	if err := Pull(context.Background(), srv.Client(), dir, &log); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("corrupt download: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "kokoro"))
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}
