package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	g2p "github.com/androiddrew/go-g2p"
	"github.com/androiddrew/go-g2p/fallback"
	"github.com/androiddrew/go-g2p/fallback/espeak"
	"github.com/androiddrew/go-g2p/fallback/neural"
	"github.com/spf13/cobra"
)

const backendHelp = "Unknown words use --fallback neural (default; bundled models, no GPL dependency)\nor --fallback espeak (requires an installed eSpeak-ng, GPL-3.0-or-later)."

func backendFlags(cmd *cobra.Command, c *config) {
	cmd.Flags().StringVar(&c.Fallback, "fallback", neural.Name, "Pronunciation fallback for unknown words: neural or espeak")
	cmd.Flags().StringVar(&c.ESpeak, "espeak", "espeak-ng", "eSpeak-ng executable (with --fallback espeak)")
}

func validateBackend(c config) error {
	if c.Fallback != neural.Name && c.Fallback != espeak.Name {
		return fmt.Errorf("--fallback must be %s or %s", neural.Name, espeak.Name)
	}
	return nil
}

func backendModelFiles(c config) []string {
	if c.Fallback == espeak.Name {
		return nil
	}
	return []string{"us.json", "us-encoder.onnx", "us-decoder.onnx", "gb.json", "gb-encoder.onnx", "gb-decoder.onnx"}
}

func backendDependencies(cmd *cobra.Command, c config) (map[string]string, error) {
	if c.Fallback == neural.Name {
		return map[string]string{"implementation": "neural ONNX", "provider": "cpu", "generation_limit": "21 tokens", "input_limit": "62 code points"}, nil
	}
	path, err := exec.LookPath(c.ESpeak)
	if err != nil {
		return nil, fmt.Errorf("--fallback espeak requires eSpeak-ng; install it or set --espeak: %w", err)
	}
	out, err := exec.CommandContext(cmd.Context(), path, "--version").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("eSpeak-ng version: %w: %s", err, out)
	}
	return map[string]string{"implementation": "external eSpeak-ng", "executable": path, "version": strings.TrimSpace(string(out)), "license": "GPL-3.0-or-later"}, nil
}

// frontend is a G2P engine together with the fallback it owns.
type frontend struct {
	*g2p.Engine
	fallback fallback.Engine
}

func newFrontend(c config) (_ *frontend, err error) {
	f := &frontend{}
	defer func() {
		if err != nil {
			err = errors.Join(err, f.close())
		}
	}()
	if c.Fallback == espeak.Name {
		f.fallback, err = espeak.New(espeak.Config{Executable: c.ESpeak})
	} else {
		f.fallback, err = neural.New(neural.Config{ModelDir: c.Frontend.ModelDir, ORTLibrary: c.Frontend.ORTLibrary})
	}
	if err != nil {
		return nil, err
	}
	fc := c.Frontend
	fc.Fallback = f.fallback
	f.Engine, err = g2p.New(fc)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// close shuts the engine before the fallback it uses.
func (f *frontend) close() error {
	var err error
	if f.Engine != nil {
		err = f.Engine.Close()
	}
	if f.fallback != nil {
		err = errors.Join(err, f.fallback.Close())
	}
	return err
}
