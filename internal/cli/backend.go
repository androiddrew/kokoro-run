package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/androiddrew/go-g2p/fallback/espeak"
	"github.com/androiddrew/go-g2p/fallback/neural"
	"github.com/spf13/cobra"

	"github.com/androiddrew/kokoro-run/internal/engine"
)

const backendHelp = "Unknown words use --fallback neural (default; bundled models, no GPL dependency)\nor --fallback espeak (requires an installed eSpeak-ng, GPL-3.0-or-later)."

func backendFlags(cmd *cobra.Command) {
	cmd.Flags().String("fallback", neural.Name, "Pronunciation fallback for unknown words: neural or espeak")
	cmd.Flags().String("espeak", "espeak-ng", "eSpeak-ng executable (with --fallback espeak)")
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

// options converts c for engine.Load and engine.NewFrontend.
func (c config) options(controlled bool) engine.Options {
	return engine.Options{Frontend: c.Frontend, Synthesis: c.Synthesis, Fallback: c.Fallback, ESpeak: c.ESpeak, Controlled: controlled}
}
