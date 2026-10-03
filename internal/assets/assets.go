// Package assets downloads and verifies the Kokoro model files listed in
// manifest.json: their URLs, sizes and SHA-256 sums.
package assets

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed manifest.json
var manifest []byte

// File is one manifest entry.
type File struct {
	Path   string `json:"path"` // relative to the assets directory
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	URL    string `json:"url"`
}

// Files lists the manifest.
func Files() ([]File, error) {
	var m struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("asset manifest: %w", err)
	}
	return m.Files, nil
}

// Verify checks that the file at path has f's size and SHA-256.
func (f File) Verify(path string) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	info, err := r.Stat()
	if err != nil {
		return err
	}
	if info.Size() != f.Bytes {
		return fmt.Errorf("%s is %d bytes, want %d", path, info.Size(), f.Bytes)
	}
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("%s has SHA-256 %s, want %s", path, got, f.SHA256)
	}
	return nil
}

// Pull makes every manifest file present and verified under dir. A file
// already there is kept if it verifies; otherwise it is downloaded to a
// temporary file beside it, checked, and renamed into place, so a failed
// download never leaves a partial file under the final name. progress gets
// a line per file.
func Pull(ctx context.Context, client *http.Client, dir string, progress io.Writer) error {
	files, err := Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := f.Verify(path); err == nil {
			fmt.Fprintf(progress, "%s: present and verified\n", path)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(progress, "%s: %v; downloading again\n", path, err)
		}
		fmt.Fprintf(progress, "%s: downloading %d MB from %s\n", path, f.Bytes>>20, f.URL)
		if err := download(ctx, client, f, path); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		fmt.Fprintf(progress, "%s: verified\n", path)
	}
	return nil
}

func download(ctx context.Context, client *http.Client, f File, path string) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", f.URL, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, f.Bytes+1))
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return err
	}
	if n != f.Bytes {
		return fmt.Errorf("downloaded %d bytes, want %d", n, f.Bytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("downloaded SHA-256 %s, want %s", got, f.SHA256)
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
