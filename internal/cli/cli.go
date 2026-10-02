// Package cli implements the standalone, compile-time-selected English TTS CLI.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/spf13/cobra"
)

type config struct {
	Frontend  g2p.Config
	Synthesis kokoro.Config
	Fallback  string // neural or espeak
	ESpeak    string // eSpeak-ng executable
	Language  string
	Voice     string
	Speed     float32
	Trim      bool
}

type inputFlags struct{ text, file string }

func NewCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "kokoro-run", Short: "Local US/UK English text-to-speech",
		Long:         "Local US/UK English text-to-speech using Kokoro and a user-installed ONNX Runtime.\n" + backendHelp,
		Version:      buildVersion(),
		SilenceUsage: true, SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(synthCommand(), phonemizeCommand(), voicesCommand(), doctorCommand(), doctorWorkerCommand(), benchCommand())
	return root
}

func frontendFlags(cmd *cobra.Command, c *config) {
	f := cmd.Flags()
	f.StringVar(&c.Frontend.DataDir, "data", "", "Override the built-in frontend dictionary/tokenizer data with this directory")
	f.StringVar(&c.Frontend.ModelDir, "frontend-models", "", "Override the built-in POS and neural fallback models with this directory")
	f.StringVar(&c.Frontend.ORTLibrary, "ort", os.Getenv("KOKORO_RUN_ORT_LIBRARY"), "ONNX Runtime library path or OS-loader name (or KOKORO_RUN_ORT_LIBRARY)")
	backendFlags(cmd, c)
}

func synthesisFlags(cmd *cobra.Command, c *config) {
	f := cmd.Flags()
	f.StringVar(&c.Synthesis.ModelPath, "model", assetPath("kokoro", "kokoro-v1.0.onnx"), "Kokoro v1.0 ONNX model")
	f.StringVar(&c.Synthesis.VoicesPath, "voices", assetPath("kokoro", "voices-v1.0.bin"), "Voice NPZ archive")
	f.StringVar(&c.Synthesis.VocabPath, "vocab", "", "Kokoro vocabulary JSON override (default: embedded Kokoro v1.0)")
	f.StringVar((*string)(&c.Synthesis.Provider), "provider", "cpu", "Kokoro provider: cpu or cuda (frontend always uses CPU)")
	f.IntVar(&c.Synthesis.DeviceID, "device", 0, "CUDA device ID")
	f.IntVar(&c.Synthesis.Threads, "threads", 1, "Kokoro intra-op threads (inter-op and frontend fixed at 1)")
	f.BoolVar(&c.Synthesis.Verbose, "verbose", false, "Write ORT placement diagnostics to stderr")
	f.StringVar(&c.Voice, "voice", "", "English voice (default: af_heart for us, bf_emma for gb)")
	f.Float32Var(&c.Speed, "speed", 1, "Speech speed in [0.5, 2.0]")
	f.BoolVar(&c.Trim, "trim", true, "Trim leading/trailing audio more than 60 dB below each chunk's peak")
}

func languageFlag(cmd *cobra.Command, c *config) {
	cmd.Flags().StringVar(&c.Language, "language", "us", "English dialect: us or gb")
}

// KOKORO_RUN_ASSETS selects a complete runtime bundle. Individual flags still override
// these defaults, so deployed binaries do not depend on a workspace layout.
func assetPath(parts ...string) string {
	root := os.Getenv("KOKORO_RUN_ASSETS")
	if root == "" {
		root = "assets"
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func textFlags(cmd *cobra.Command, input *inputFlags) {
	cmd.Flags().StringVar(&input.text, "text", "", "Input text, including Misaki pronunciation overrides")
	cmd.Flags().StringVar(&input.file, "file", "", "UTF-8 input file; - reads stdin (stdin is also the default)")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
}

func (c *config) validate(synthesis bool) error {
	if c.Language != "us" && c.Language != "gb" {
		return errors.New("--language must be us or gb")
	}
	if err := validateBackend(*c); err != nil {
		return err
	}
	if c.Frontend.ORTLibrary == "" {
		return errors.New("provide --ort with a library path or loader name (e.g. libonnxruntime.so), or set KOKORO_RUN_ORT_LIBRARY")
	}
	if !synthesis {
		return nil
	}
	c.Synthesis.ORTLibrary = c.Frontend.ORTLibrary
	if c.Synthesis.Provider != "cpu" && c.Synthesis.Provider != "cuda" {
		return errors.New("--provider must be cpu or cuda")
	}
	if c.Synthesis.Threads < 1 || c.Synthesis.DeviceID < 0 {
		return errors.New("--threads must be positive and --device nonnegative")
	}
	if c.Synthesis.Provider == "cpu" && c.Synthesis.DeviceID != 0 {
		return errors.New("--device is only applicable to --provider cuda")
	}
	if math.IsNaN(float64(c.Speed)) || math.IsInf(float64(c.Speed), 0) || c.Speed < 0.5 || c.Speed > 2 {
		return errors.New("--speed must be finite and in [0.5, 2.0]")
	}
	if c.Voice == "" {
		if c.Language == "us" {
			c.Voice = "af_heart"
		} else {
			c.Voice = "bf_emma"
		}
	}
	prefix := "a"
	if c.Language == "gb" {
		prefix = "b"
	}
	if !strings.HasPrefix(c.Voice, prefix) {
		return fmt.Errorf("voice %q does not match --language %s; run voices --language %s", c.Voice, c.Language, c.Language)
	}
	return nil
}

func readText(cmd *cobra.Command, input inputFlags) (string, error) {
	text := input.text
	if !cmd.Flags().Changed("text") {
		reader := cmd.InOrStdin()
		if input.file != "" && input.file != "-" {
			f, err := os.Open(input.file)
			if err != nil {
				return "", err
			}
			defer f.Close()
			reader = f
		}
		// The frontend's 100,000-code-point bound needs at most 400,000 UTF-8 bytes.
		data, err := io.ReadAll(io.LimitReader(reader, 400001))
		if err != nil {
			return "", err
		}
		if len(data) > 400000 {
			return "", errors.New("input exceeds 400000 bytes; split into documents")
		}
		text = string(data)
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", errors.New("text must be valid UTF-8 without NUL")
	}
	if utf8.RuneCountInString(text) > 100000 {
		return "", errors.New("text exceeds 100000 code points; split into documents")
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("input text is empty")
	}
	return text, nil
}

func encode(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

// Create exclusively; never truncate a user's file. Remove only our own partial
// output on a write/close failure. A successful Close completes file timing.
func writeNew(path string, write func(io.Writer) error) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	err = errors.Join(write(f), f.Close())
	if err != nil {
		err = errors.Join(err, os.Remove(path))
	}
	return err
}

func diagnostics(w io.Writer, result g2p.Result) error {
	for _, d := range result.Diagnostics {
		if _, err := fmt.Fprintf(w, "%s: %q: %s\n", d.Code, d.Text, d.Message); err != nil {
			return err
		}
	}
	return nil
}

func phonemizeCommand() *cobra.Command {
	var c config
	var input inputFlags
	var asJSON, allowTruncated bool
	cmd := &cobra.Command{Use: "phonemize", Short: "Convert text to phonemes and pronunciation diagnostics", Args: cobra.NoArgs}
	frontendFlags(cmd, &c)
	languageFlag(cmd, &c)
	textFlags(cmd, &input)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Include tokens, fallback calls and completeness in JSON")
	cmd.Flags().BoolVar(&allowTruncated, "allow-truncated", false, "Comparison only: permit generation-limited output, retaining diagnostics")
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		if err = c.validate(false); err != nil {
			return err
		}
		text, err := readText(cmd, input)
		if err != nil {
			return err
		}
		frontend, err := newFrontend(c)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, frontend.close()) }()
		result, runErr := frontend.Phonemize(cmd.Context(), g2p.Request{Text: text, Dialect: g2p.Dialect(c.Language), AllowTruncated: allowTruncated, Debug: asJSON})
		err = diagnostics(cmd.ErrOrStderr(), result)
		if asJSON {
			err = errors.Join(err, encode(cmd.OutOrStdout(), result))
		} else if runErr == nil {
			_, e := fmt.Fprintln(cmd.OutOrStdout(), result.Phonemes)
			err = errors.Join(err, e)
		}
		return errors.Join(runErr, err)
	}
	return cmd
}

func voicesCommand() *cobra.Command {
	var path, language string
	var asJSON bool
	cmd := &cobra.Command{Use: "voices", Short: "List supported English voices without loading ONNX Runtime", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&path, "voices", assetPath("kokoro", "voices-v1.0.bin"), "Voice NPZ archive")
	cmd.Flags().StringVar(&language, "language", "", "Filter by us or gb (default: both)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Write JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if language != "" && language != "us" && language != "gb" {
			return errors.New("--language must be us or gb")
		}
		voices, err := kokoro.ListVoices(path)
		if err != nil {
			return err
		}
		names := []string{}
		for _, name := range voices {
			if (language != "gb" && strings.HasPrefix(name, "a")) || (language != "us" && strings.HasPrefix(name, "b")) {
				names = append(names, name)
			}
		}
		if asJSON {
			return encode(cmd.OutOrStdout(), names)
		}
		for _, name := range names {
			if _, err = fmt.Fprintln(cmd.OutOrStdout(), name); err != nil {
				return err
			}
		}
		return nil
	}
	return cmd
}

func buildMetadata() map[string]string {
	result := map[string]string{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "version": buildVersion()}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			result[setting.Key] = setting.Value
		}
		for _, dep := range info.Deps {
			result[dep.Path] = dep.Version
			if dep.Path == "github.com/yalue/onnxruntime_go" {
				result["ort_binding"] = dep.Version
			}
		}
	}
	return result
}

// version is set at release time with -ldflags "-X github.com/androiddrew/kokoro-run/internal/cli.version=v0.1.0".
var version string

// buildVersion prefers the linker-set version, then the module version recorded
// by "go install module@version", then "devel".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}
