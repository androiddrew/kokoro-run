// Package config is kokoro-run's settings, layered from built-in defaults, an
// optional YAML file, KOKORO_RUN_* environment variables and command-line
// flags, each overriding the one before.
//
// Every setting's environment variable is KOKORO_RUN_ followed by its YAML path
// in upper case with "_" between the parts: server.listen is
// KOKORO_RUN_SERVER_LISTEN. A field's `flag` tag names the flag that sets it,
// which overrides the rest only when it is given on the command line.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"
)

// EnvPrefix starts every setting's environment variable.
const EnvPrefix = "KOKORO_RUN_"

// FileEnv selects the config file when --config is not given.
const FileEnv = EnvPrefix + "CONFIG"

// Model and voice archive names inside the assets directory.
var (
	ModelFile  = filepath.Join("kokoro", "kokoro-v1.0.onnx")
	VoicesFile = filepath.Join("kokoro", "voices-v1.0.bin")
)

// Config is every setting. Fields without a flag tag are set only by the file
// and the environment.
type Config struct {
	ORTLibrary     string  `yaml:"ort_library" json:"ort_library" flag:"ort"`
	Assets         string  `yaml:"assets" json:"assets"` // holds kokoro/kokoro-v1.0.onnx and kokoro/voices-v1.0.bin
	Model          string  `yaml:"model" json:"model" flag:"model"`
	VoicesFile     string  `yaml:"voices_file" json:"voices_file" flag:"voices"`
	Vocab          string  `yaml:"vocab" json:"vocab" flag:"vocab"`
	Data           string  `yaml:"data" json:"data" flag:"data"`
	FrontendModels string  `yaml:"frontend_models" json:"frontend_models" flag:"frontend-models"`
	Provider       string  `yaml:"provider" json:"provider" flag:"provider"`
	Device         int     `yaml:"device" json:"device" flag:"device"`
	Threads        int     `yaml:"threads" json:"threads" flag:"threads"`
	Verbose        bool    `yaml:"verbose" json:"verbose" flag:"verbose"`
	Fallback       string  `yaml:"fallback" json:"fallback" flag:"fallback"`
	ESpeak         string  `yaml:"espeak" json:"espeak" flag:"espeak"`
	Language       string  `yaml:"language" json:"language" flag:"language"`
	Voice          string  `yaml:"voice" json:"voice" flag:"voice"`
	Speed          float32 `yaml:"speed" json:"speed" flag:"speed"`
	Trim           bool    `yaml:"trim" json:"trim" flag:"trim"`
	Normalize      bool    `yaml:"normalize" json:"normalize" flag:"normalize"`
	Markdown       bool    `yaml:"markdown" json:"markdown" flag:"markdown"`
	Server         Server  `yaml:"server" json:"server"`
}

// Server is the HTTP API's settings.
type Server struct {
	Listen   string `yaml:"listen" json:"listen" flag:"listen"`
	Replicas int    `yaml:"replicas" json:"replicas" flag:"replicas"` // loaded pipelines; each holds its own Kokoro session
	MaxQueue int    `yaml:"max_queue" json:"max_queue" flag:"max-queue"`
	// Preload loads every replica before listening; serve exits if it can't.
	Preload bool `yaml:"preload" json:"preload" flag:"preload"`
	// StrictPronunciation fails a request with an unresolved or
	// generation-limited pronunciation instead of speaking it as far as known.
	StrictPronunciation bool              `yaml:"strict_pronunciation" json:"strict_pronunciation" flag:"strict-pronunciation"`
	ChunkGap            time.Duration     `yaml:"chunk_gap" json:"chunk_gap"` // silence between streamed sentences
	ModelAliases        map[string]string `yaml:"model_aliases" json:"model_aliases"`
	Voices              map[string]string `yaml:"voices" json:"voices"` // OpenAI voice -> Kokoro voice
	Speed               SpeedRange        `yaml:"speed" json:"speed"`
	Limits              Limits            `yaml:"limits" json:"limits"`
	FFmpeg              string            `yaml:"ffmpeg" json:"ffmpeg"` // path or name on PATH; empty disables opus, aac and flac
	Metrics             bool              `yaml:"metrics" json:"metrics"`
	LogFormat           string            `yaml:"log_format" json:"log_format" flag:"log-format"`
	APIKeyFile          string            `yaml:"api_key_file" json:"api_key_file"`
	// APIKey comes only from KOKORO_RUN_API_KEY or APIKeyFile, never the file
	// itself, so a config can be shared without its secret.
	APIKey string `yaml:"-" json:"-" env:"KOKORO_RUN_API_KEY"`
}

// SpeedRange is the range a request's speed is clamped to.
type SpeedRange struct {
	Min float32 `yaml:"min" json:"min"`
	Max float32 `yaml:"max" json:"max"`
}

// Limits are per-request limits.
type Limits struct {
	MaxInputChars  int           `yaml:"max_input_chars" json:"max_input_chars"`
	RequestTimeout time.Duration `yaml:"request_timeout" json:"request_timeout"` // covers queue wait and synthesis
}

// ModelName is the one model the server offers.
const ModelName = "kokoro-v1.0"

// Default returns the built-in settings.
func Default() Config {
	return Config{
		Assets:    "assets",
		Provider:  "cpu",
		Threads:   1,
		Fallback:  "neural",
		ESpeak:    "espeak-ng",
		Language:  "us",
		Speed:     1,
		Trim:      true,
		Normalize: true,
		Markdown:  true,
		Server: Server{
			Listen:    "127.0.0.1:8880",
			Replicas:  1,
			MaxQueue:  8,
			Preload:   true,
			ChunkGap:  120 * time.Millisecond,
			Speed:     SpeedRange{Min: 0.5, Max: 2},
			Limits:    Limits{MaxInputChars: 4096, RequestTimeout: 120 * time.Second},
			FFmpeg:    "ffmpeg",
			Metrics:   true,
			LogFormat: "json",
		},
	}
}

// DefaultModelAliases are the OpenAI model names that select ModelName.
func DefaultModelAliases() map[string]string {
	return map[string]string{"tts-1": ModelName, "tts-1-hd": ModelName, "gpt-4o-mini-tts": ModelName}
}

// DefaultVoices maps OpenAI's voices to Kokoro voices. Kokoro has five of
// OpenAI's names; the rest are matched by rough character.
func DefaultVoices() map[string]string {
	return map[string]string{
		"alloy": "af_alloy", "nova": "af_nova", "echo": "am_echo", "onyx": "am_onyx", "fable": "bm_fable",
		"shimmer": "af_bella", "coral": "af_heart", "sage": "af_sarah", "marin": "af_jessica",
		"ash": "am_adam", "ballad": "am_michael", "cedar": "am_eric", "verse": "am_puck",
	}
}

// Source is where a setting's value came from.
type Source string

const (
	FromDefault Source = "default"
	FromFile    Source = "file"
	FromEnv     Source = "env"
	FromFlag    Source = "flag"
)

// Loaded is a resolved config and where each setting came from, by YAML path.
type Loaded struct {
	Config
	File    string
	Sources map[string]Source
}

// Load layers the file at path (none if empty), the environment from getenv
// and the changed flags in flags (may be nil) over the defaults. It doesn't
// validate the result; see Validate.
func Load(path string, getenv func(string) string, flags *pflag.FlagSet) (*Loaded, error) {
	l := &Loaded{Config: Default(), File: path, Sources: map[string]Source{}}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = l.decode(data); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	var errs []error
	walk(reflect.ValueOf(&l.Config).Elem(), "", func(key string, f reflect.StructField, v reflect.Value) {
		name := f.Tag.Get("env")
		if name == "" {
			name = EnvName(key)
		}
		if s := getenv(name); s != "" {
			if err := set(v, s); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
			l.Sources[key] = FromEnv
		}
		if flag := f.Tag.Get("flag"); flags != nil && flag != "" {
			if fl := flags.Lookup(flag); fl != nil && fl.Changed {
				if err := set(v, fl.Value.String()); err != nil {
					errs = append(errs, fmt.Errorf("--%s: %w", flag, err))
				}
				l.Sources[key] = FromFlag
			}
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	l.fill()
	if l.Server.APIKey == "" && l.Server.APIKeyFile != "" {
		key, err := os.ReadFile(l.Server.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("server.api_key_file: %w", err)
		}
		l.Server.APIKey = strings.TrimSpace(string(key))
	}
	return l, nil
}

// decode reads a YAML file over the defaults, rejecting unknown keys, and
// records which settings it set.
func (l *Loaded) decode(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l.Config); err != nil && err != io.EOF {
		return err
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return err
	}
	walk(reflect.ValueOf(&l.Config).Elem(), "", func(key string, _ reflect.StructField, _ reflect.Value) {
		if lookup(tree, strings.Split(key, ".")) {
			l.Sources[key] = FromFile
		}
	})
	return nil
}

func lookup(tree map[string]any, path []string) bool {
	v, ok := tree[path[0]]
	if !ok || len(path) == 1 {
		return ok
	}
	sub, ok := v.(map[string]any)
	return ok && lookup(sub, path[1:])
}

// fill derives the settings that default from others.
func (l *Loaded) fill() {
	// A voice given without a language selects its own.
	if _, set := l.Sources["language"]; !set {
		if lang, ok := VoiceLanguage(l.Voice); ok && l.Voice != "" {
			l.Language = lang
		}
	}
	if l.Model == "" {
		l.Model = filepath.Join(l.Assets, ModelFile)
	}
	if l.VoicesFile == "" {
		l.VoicesFile = filepath.Join(l.Assets, VoicesFile)
	}
	if l.Server.ModelAliases == nil {
		l.Server.ModelAliases = DefaultModelAliases()
	}
	if l.Server.Voices == nil {
		l.Server.Voices = DefaultVoices()
	}
}

// EnvName is the environment variable of the setting at a YAML path.
func EnvName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// Keys lists every setting's YAML path, in field order.
func Keys() []string {
	var keys []string
	c := Default()
	walk(reflect.ValueOf(&c).Elem(), "", func(key string, _ reflect.StructField, _ reflect.Value) { keys = append(keys, key) })
	return keys
}

// walk calls fn for each setting in the struct v, by YAML path. Structs are
// descended into; durations, maps and scalars are settings. A field with
// yaml:"-" is a setting under its env tag only.
func walk(v reflect.Value, prefix string, fn func(key string, f reflect.StructField, v reflect.Value)) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" {
			if f.Tag.Get("env") != "" {
				fn(prefix+strings.ToLower(f.Name), f, v.Field(i))
			}
			continue
		}
		key := prefix + name
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeFor[time.Duration]() {
			walk(v.Field(i), key+".", fn)
			continue
		}
		fn(key, f, v.Field(i))
	}
}

// set parses s into v: a string, bool, int, float, duration or a map written
// as k=v,k=v.
func set(v reflect.Value, s string) error {
	switch {
	case v.Type() == reflect.TypeFor[time.Duration]():
		d, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	case v.Kind() == reflect.String:
		v.SetString(s)
	case v.Kind() == reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v.SetBool(b)
	case v.Kind() == reflect.Int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(n))
	case v.Kind() == reflect.Float32:
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return err
		}
		v.SetFloat(f)
	case v.Kind() == reflect.Map:
		m := map[string]string{}
		for pair := range strings.SplitSeq(s, ",") {
			k, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || k == "" || val == "" {
				return fmt.Errorf("%q is not a key=value list", s)
			}
			m[k] = val
		}
		v.Set(reflect.ValueOf(m))
	default:
		return fmt.Errorf("unsupported setting type %s", v.Type())
	}
	return nil
}

// Validate checks the settings every command uses and, with synthesis, those
// that loading Kokoro needs. It sets Voice to the language's default voice
// when it is empty. Errors are joined so one run reports them all.
func (c *Config) Validate(synthesis bool) error {
	var errs []error
	if c.Language != "us" && c.Language != "gb" {
		errs = append(errs, errors.New("language must be us or gb"))
	}
	if c.Fallback != "neural" && c.Fallback != "espeak" {
		errs = append(errs, errors.New("fallback must be neural or espeak"))
	}
	if c.ORTLibrary == "" {
		errs = append(errs, errors.New("provide --ort with a library path or loader name (e.g. libonnxruntime.so), or set KOKORO_RUN_ORT_LIBRARY or ort_library"))
	}
	if !synthesis {
		return errors.Join(errs...)
	}
	if c.Provider != "cpu" && c.Provider != "cuda" {
		errs = append(errs, errors.New("provider must be cpu or cuda"))
	}
	if c.Threads < 1 || c.Device < 0 {
		errs = append(errs, errors.New("threads must be positive and device nonnegative"))
	}
	if c.Provider == "cpu" && c.Device != 0 {
		errs = append(errs, errors.New("device is only applicable to provider cuda"))
	}
	if !validSpeed(c.Speed) {
		errs = append(errs, errors.New("speed must be finite and in [0.5, 2.0]"))
	}
	if c.Voice == "" {
		c.Voice = DefaultVoice(c.Language)
	}
	if c.Language == "us" || c.Language == "gb" {
		if lang, ok := VoiceLanguage(c.Voice); !ok || lang != c.Language {
			errs = append(errs, fmt.Errorf("voice %q does not match language %s; run voices --language %s", c.Voice, c.Language, c.Language))
		}
	}
	return errors.Join(errs...)
}

// ValidateServer checks the server settings, given the voices the archive
// holds.
func (c *Config) ValidateServer(voices []string) error {
	var errs []error
	s := c.Server
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		errs = append(errs, fmt.Errorf("server.listen: %w", err))
	}
	if s.Replicas < 1 {
		errs = append(errs, fmt.Errorf("server.replicas must be positive, got %d", s.Replicas))
	}
	if s.MaxQueue < 0 {
		errs = append(errs, fmt.Errorf("server.max_queue must not be negative, got %d", s.MaxQueue))
	}
	if s.ChunkGap < 0 || s.ChunkGap > 2*time.Second {
		errs = append(errs, fmt.Errorf("server.chunk_gap must be 0 to 2s, got %v", s.ChunkGap))
	}
	if !validSpeed(s.Speed.Min) || !validSpeed(s.Speed.Max) || s.Speed.Min > s.Speed.Max {
		errs = append(errs, fmt.Errorf("server.speed must have 0.5 <= min <= max <= 2.0, got %v to %v", s.Speed.Min, s.Speed.Max))
	}
	if s.Limits.MaxInputChars <= 0 || s.Limits.MaxInputChars > 100000 {
		errs = append(errs, fmt.Errorf("server.limits.max_input_chars must be 1 to 100000, got %d", s.Limits.MaxInputChars))
	}
	if s.Limits.RequestTimeout <= 0 {
		errs = append(errs, fmt.Errorf("server.limits.request_timeout must be positive, got %v", s.Limits.RequestTimeout))
	}
	if s.LogFormat != "json" && s.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("server.log_format must be json or text, got %q", s.LogFormat))
	}
	if s.APIKey != "" && strings.TrimSpace(s.APIKey) == "" {
		errs = append(errs, errors.New("the API key is only whitespace; unset it to turn auth off"))
	}
	for alias, target := range s.ModelAliases {
		if target != ModelName {
			errs = append(errs, fmt.Errorf("server.model_aliases: %s maps to %s; the only model is %s", alias, target, ModelName))
		}
	}
	for openai, voice := range s.Voices {
		if !slices.Contains(voices, voice) {
			errs = append(errs, fmt.Errorf("server.voices: %s maps to %s, which is not in the voice archive", openai, voice))
		} else if _, ok := VoiceLanguage(voice); !ok {
			errs = append(errs, fmt.Errorf("server.voices: %s maps to %s, which is not a US or UK English voice", openai, voice))
		}
	}
	return errors.Join(errs...)
}

func validSpeed(s float32) bool {
	return !math.IsNaN(float64(s)) && !math.IsInf(float64(s), 0) && s >= 0.5 && s <= 2
}

// DefaultVoice is a language's voice when none is chosen.
func DefaultVoice(language string) string {
	if language == "gb" {
		return "bf_emma"
	}
	return "af_heart"
}

// VoiceLanguage is the English dialect of a Kokoro voice: "a" voices are us
// and "b" voices are gb.
func VoiceLanguage(voice string) (string, bool) {
	switch {
	case strings.HasPrefix(voice, "a"):
		return "us", true
	case strings.HasPrefix(voice, "b"):
		return "gb", true
	}
	return "", false
}
