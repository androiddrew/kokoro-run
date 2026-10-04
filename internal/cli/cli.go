// Package cli implements the standalone, compile-time-selected English TTS CLI.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/spf13/cobra"

	settings "github.com/androiddrew/kokoro-run/internal/config"
	"github.com/androiddrew/kokoro-run/internal/engine"
	"github.com/androiddrew/kokoro-run/internal/textprep"
)

// config is one run's resolved settings in the form the engines take.
type config struct {
	Frontend  g2p.Config
	Synthesis kokoro.Config
	Fallback  string // neural or espeak
	ESpeak    string // eSpeak-ng executable
	Language  string
	Voice     string
	Speed     float32
	Trim      bool
	Normalize bool // read numbers, dates, times, URLs and the like as words
	Markdown  bool // read markdown as prose
}

// prepare applies the configured markdown and normalization steps.
func (c config) prepare(text string) string {
	return textprep.Text(text, textprep.Options{Markdown: c.Markdown, Normalize: c.Normalize})
}

// fromSettings converts layered settings to a run's config.
func fromSettings(s settings.Config) config {
	return config{
		Frontend: g2p.Config{DataDir: s.Data, ModelDir: s.FrontendModels, ORTLibrary: s.ORTLibrary},
		Synthesis: kokoro.Config{
			ORTLibrary: s.ORTLibrary, ModelPath: s.Model, VoicesPath: s.VoicesFile, VocabPath: s.Vocab,
			Provider: kokoro.Provider(s.Provider), DeviceID: s.Device, Threads: s.Threads, Verbose: s.Verbose,
		},
		Fallback: s.Fallback, ESpeak: s.ESpeak, Language: s.Language, Voice: s.Voice, Speed: s.Speed,
		Trim: s.Trim, Normalize: s.Normalize, Markdown: s.Markdown,
	}
}

// settings converts a run's config back, for validation.
func (c config) settings() settings.Config {
	s := settings.Default()
	s.ORTLibrary, s.Data, s.FrontendModels = c.Frontend.ORTLibrary, c.Frontend.DataDir, c.Frontend.ModelDir
	s.Model, s.VoicesFile, s.Vocab = c.Synthesis.ModelPath, c.Synthesis.VoicesPath, c.Synthesis.VocabPath
	s.Provider, s.Device, s.Threads, s.Verbose = string(c.Synthesis.Provider), c.Synthesis.DeviceID, c.Synthesis.Threads, c.Synthesis.Verbose
	s.Fallback, s.ESpeak, s.Language, s.Voice, s.Speed = c.Fallback, c.ESpeak, c.Language, c.Voice, c.Speed
	s.Trim, s.Normalize, s.Markdown = c.Trim, c.Normalize, c.Markdown
	return s
}

// loadSettings layers the config file, the environment and cmd's changed
// flags over the defaults.
func loadSettings(cmd *cobra.Command) (*settings.Loaded, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		path = os.Getenv(settings.FileEnv)
	}
	return settings.Load(path, os.Getenv, cmd.Flags())
}

// resolve loads and validates cmd's settings as a run's config.
func resolve(cmd *cobra.Command, synthesis bool) (config, error) {
	l, err := loadSettings(cmd)
	if err != nil {
		return config{}, err
	}
	c := fromSettings(l.Config)
	return c, c.validate(synthesis)
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
	root.PersistentFlags().String("config", "", "YAML config file (or "+settings.FileEnv+"); environment variables and flags override it")
	root.AddCommand(synthCommand(), phonemizeCommand(), voicesCommand(), doctorCommand(), doctorWorkerCommand(), benchCommand(), configCommand(), serveCommand(), pullCommand(), healthcheckCommand())
	return root
}

// The flags below only declare settings: a command reads them through
// resolve, which applies a flag only when it is given, over the config file
// and the environment. Their defaults are settings.Default's, for --help.

func frontendFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("data", "", "Override the built-in frontend dictionary/tokenizer data with this directory")
	f.String("frontend-models", "", "Override the built-in POS and neural fallback models with this directory")
	f.String("ort", "", "ONNX Runtime library path or OS-loader name (or KOKORO_RUN_ORT_LIBRARY)")
	backendFlags(cmd)
}

func synthesisFlags(cmd *cobra.Command) {
	d := settings.Default()
	f := cmd.Flags()
	f.String("model", "", "Kokoro v1.0 ONNX model (default: $KOKORO_RUN_ASSETS/"+settings.ModelFile+", assets defaulting to ./assets)")
	voicesFlag(cmd)
	f.String("vocab", "", "Kokoro vocabulary JSON override (default: embedded Kokoro v1.0)")
	f.String("provider", d.Provider, "Kokoro provider: cpu or cuda (frontend always uses CPU)")
	f.Int("device", d.Device, "CUDA device ID")
	f.Int("threads", d.Threads, "Kokoro intra-op threads (inter-op and frontend fixed at 1)")
	f.Bool("verbose", d.Verbose, "Write ORT placement diagnostics to stderr")
	f.String("voice", d.Voice, "English voice (default: af_heart for us, bf_emma for gb)")
	f.Float32("speed", d.Speed, "Speech speed in [0.5, 2.0]")
	f.Bool("trim", d.Trim, "Trim leading/trailing audio more than 60 dB below each chunk's peak")
}

func voicesFlag(cmd *cobra.Command) {
	cmd.Flags().String("voices", "", "Voice NPZ archive (default: $KOKORO_RUN_ASSETS/"+settings.VoicesFile+")")
}

func languageFlag(cmd *cobra.Command) {
	cmd.Flags().String("language", settings.Default().Language, "English dialect: us or gb")
}

func textPrepFlags(cmd *cobra.Command) {
	d := settings.Default()
	cmd.Flags().Bool("normalize", d.Normalize, "Read numbers, dates, times, money, units, URLs and the like as words")
	cmd.Flags().Bool("markdown", d.Markdown, "Read markdown as prose: drop markup and code blocks, keep pronunciation overrides")
}

func serverFlags(cmd *cobra.Command) {
	d := settings.Default().Server
	f := cmd.Flags()
	f.String("listen", d.Listen, "Address to serve the HTTP API on; :8880 serves every interface")
	f.Int("replicas", d.Replicas, "Pipelines that synthesize at once; each loads its own Kokoro session (~330 MB)")
	f.Int("max-queue", d.MaxQueue, "Requests that may wait for a pipeline before the server answers 429")
	f.Bool("preload", d.Preload, "Load every pipeline before listening, and exit if one fails")
	f.Bool("strict-pronunciation", d.StrictPronunciation, "Fail requests with unresolved pronunciations instead of speaking what is known")
	f.String("log-format", d.LogFormat, "Request log format: json or text")
}

func textFlags(cmd *cobra.Command, input *inputFlags) {
	cmd.Flags().StringVar(&input.text, "text", "", "Input text, including Misaki pronunciation overrides")
	cmd.Flags().StringVar(&input.file, "file", "", "UTF-8 input file; - reads stdin (stdin is also the default)")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
}

// validate checks c with settings.Config.Validate, filling in the default
// voice.
func (c *config) validate(synthesis bool) error {
	s := c.settings()
	err := s.Validate(synthesis)
	c.Voice = s.Voice
	c.Synthesis.ORTLibrary = c.Frontend.ORTLibrary
	return err
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
	frontendFlags(cmd)
	languageFlag(cmd)
	textPrepFlags(cmd)
	textFlags(cmd, &input)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Include the prepared text, tokens, fallback calls and completeness in JSON")
	cmd.Flags().BoolVar(&allowTruncated, "allow-truncated", false, "Comparison only: permit generation-limited output, retaining diagnostics")
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		if c, err = resolve(cmd, false); err != nil {
			return err
		}
		text, err := readText(cmd, input)
		if err != nil {
			return err
		}
		frontend, err := engine.NewFrontend(c.options(false))
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, frontend.Close()) }()
		prepared := c.prepare(text)
		result, runErr := frontend.Phonemize(cmd.Context(), g2p.Request{Text: prepared, Dialect: g2p.Dialect(c.Language), AllowTruncated: allowTruncated, Debug: asJSON})
		err = diagnostics(cmd.ErrOrStderr(), result)
		if asJSON {
			err = errors.Join(err, encode(cmd.OutOrStdout(), struct {
				PreparedText string `json:"prepared_text"`
				g2p.Result
			}{prepared, result}))
		} else if runErr == nil {
			_, e := fmt.Fprintln(cmd.OutOrStdout(), result.Phonemes)
			err = errors.Join(err, e)
		}
		return errors.Join(runErr, err)
	}
	return cmd
}

func voicesCommand() *cobra.Command {
	var language string
	var asJSON bool
	cmd := &cobra.Command{Use: "voices", Short: "List supported English voices without loading ONNX Runtime", Args: cobra.NoArgs}
	voicesFlag(cmd)
	cmd.Flags().StringVar(&language, "language", "", "Filter by us or gb (default: both)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Write JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if language != "" && language != "us" && language != "gb" {
			return errors.New("--language must be us or gb")
		}
		l, err := loadSettings(cmd)
		if err != nil {
			return err
		}
		voices, err := kokoro.ListVoices(l.VoicesFile)
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
