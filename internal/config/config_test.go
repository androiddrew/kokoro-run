package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestPrecedence(t *testing.T) {
	file := writeFile(t, "voice: af_bella\nspeed: 1.2\nassets: /from/file\nserver:\n  listen: \":9000\"\n  max_queue: 2\n  chunk_gap: 50ms\n")
	flags := pflag.NewFlagSet("t", pflag.ContinueOnError)
	flags.String("voice", "", "")
	flags.Float32("speed", 1, "")
	flags.String("listen", "", "")
	flags.String("unrelated", "", "")

	for _, c := range []struct {
		name    string
		path    string
		env     map[string]string
		args    []string
		voice   string
		speed   float32
		listen  string
		sources map[string]Source
	}{
		{"defaults", "", nil, nil, "", 1, "127.0.0.1:8880", map[string]Source{"voice": "", "speed": ""}},
		{"file", file, nil, nil, "af_bella", 1.2, ":9000", map[string]Source{"voice": FromFile, "server.listen": FromFile}},
		{"env over file", file, map[string]string{"KOKORO_RUN_VOICE": "af_nova", "KOKORO_RUN_SERVER_LISTEN": ":9100"}, nil,
			"af_nova", 1.2, ":9100", map[string]Source{"voice": FromEnv, "speed": FromFile, "server.listen": FromEnv}},
		{"flag over env", file, map[string]string{"KOKORO_RUN_VOICE": "af_nova", "KOKORO_RUN_SPEED": "0.9"}, []string{"--voice", "am_adam", "--listen", ":9200"},
			"am_adam", 0.9, ":9200", map[string]Source{"voice": FromFlag, "speed": FromEnv, "server.listen": FromFlag}},
		{"unchanged flag keeps env", "", map[string]string{"KOKORO_RUN_SPEED": "1.5"}, []string{"--unrelated", "x"}, "", 1.5, "127.0.0.1:8880", map[string]Source{"speed": FromEnv}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
			fs.AddFlagSet(flags)
			fs.VisitAll(func(f *pflag.Flag) { f.Changed = false; _ = f.Value.Set(f.DefValue) })
			if err := fs.Parse(c.args); err != nil {
				t.Fatal(err)
			}
			l, err := Load(c.path, env(c.env), fs)
			if err != nil {
				t.Fatal(err)
			}
			if l.Voice != c.voice || l.Speed != c.speed || l.Server.Listen != c.listen {
				t.Errorf("got voice %q speed %v listen %q, want %q %v %q", l.Voice, l.Speed, l.Server.Listen, c.voice, c.speed, c.listen)
			}
			for key, want := range c.sources {
				if got := l.Sources[key]; got != want {
					t.Errorf("source of %s = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestDerivedAndTypedSettings(t *testing.T) {
	l, err := Load(writeFile(t, "assets: /data\nserver:\n  chunk_gap: 50ms\n  limits:\n    request_timeout: 30s\n"), env(map[string]string{
		"KOKORO_RUN_SERVER_VOICES":                 "alloy=af_bella,echo=am_adam",
		"KOKORO_RUN_SERVER_LIMITS_MAX_INPUT_CHARS": "100",
		"KOKORO_RUN_TRIM":                          "false",
		"KOKORO_RUN_API_KEY":                       "secret",
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Model != "/data/kokoro/kokoro-v1.0.onnx" || l.VoicesFile != "/data/kokoro/voices-v1.0.bin" {
		t.Errorf("asset paths: %q %q", l.Model, l.VoicesFile)
	}
	if l.Server.ChunkGap != 50*time.Millisecond || l.Server.Limits.RequestTimeout != 30*time.Second || l.Server.Limits.MaxInputChars != 100 {
		t.Errorf("typed settings: %+v", l.Server)
	}
	if len(l.Server.Voices) != 2 || l.Server.Voices["echo"] != "am_adam" || l.Trim || l.Server.APIKey != "secret" {
		t.Errorf("env settings: voices %v trim %v key %q", l.Server.Voices, l.Trim, l.Server.APIKey)
	}
	if l.Server.ModelAliases["tts-1"] != ModelName {
		t.Errorf("default aliases: %v", l.Server.ModelAliases)
	}
}

func TestVoiceSelectsLanguage(t *testing.T) {
	l, err := Load("", env(map[string]string{"KOKORO_RUN_VOICE": "bf_emma", "KOKORO_RUN_ORT_LIBRARY": "x"}), nil)
	if err != nil || l.Language != "gb" || l.Validate(true) != nil {
		t.Fatalf("voice alone: language %q, %v", l.Language, err)
	}
	l, err = Load("", env(map[string]string{"KOKORO_RUN_VOICE": "bf_emma", "KOKORO_RUN_LANGUAGE": "us", "KOKORO_RUN_ORT_LIBRARY": "x"}), nil)
	if err != nil || l.Language != "us" || l.Validate(true) == nil {
		t.Fatalf("an explicit language must still conflict: %q, %v", l.Language, err)
	}
}

func TestAPIKeyFile(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(writeFile(t, "server:\n  api_key_file: "+key+"\n"), env(nil), nil)
	if err != nil || l.Server.APIKey != "from-file" {
		t.Fatalf("key %q, %v", l.Server.APIKey, err)
	}
	l, err = Load(writeFile(t, "server:\n  api_key_file: "+key+"\n"), env(map[string]string{"KOKORO_RUN_API_KEY": "env"}), nil)
	if err != nil || l.Server.APIKey != "env" {
		t.Fatalf("env key should win: %q, %v", l.Server.APIKey, err)
	}
}

func TestLoadErrors(t *testing.T) {
	for name, c := range map[string]struct {
		file string
		env  map[string]string
		want string
	}{
		"unknown key":      {"voise: af_heart\n", nil, "voise"},
		"inline api key":   {"server:\n  api_key: x\n", nil, "api_key"},
		"bad env int":      {"", map[string]string{"KOKORO_RUN_THREADS": "many"}, "KOKORO_RUN_THREADS"},
		"bad env map":      {"", map[string]string{"KOKORO_RUN_SERVER_VOICES": "alloy"}, "KOKORO_RUN_SERVER_VOICES"},
		"bad env duration": {"", map[string]string{"KOKORO_RUN_SERVER_CHUNK_GAP": "soon"}, "CHUNK_GAP"},
	} {
		t.Run(name, func(t *testing.T) {
			path := ""
			if c.file != "" {
				path = writeFile(t, c.file)
			}
			_, err := Load(path, env(c.env), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want mention of %q", err, c.want)
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), env(nil), nil); err == nil {
		t.Fatal("accepted a missing file")
	}
}

func TestValidate(t *testing.T) {
	ok := Default()
	ok.ORTLibrary = "libonnxruntime.so"
	if err := ok.Validate(true); err != nil || ok.Voice != "af_heart" {
		t.Fatalf("defaults: voice %q, %v", ok.Voice, err)
	}
	gb := ok
	gb.Language, gb.Voice = "gb", ""
	if err := gb.Validate(true); err != nil || gb.Voice != "bf_emma" {
		t.Fatalf("gb default: %q %v", gb.Voice, err)
	}
	for name, mutate := range map[string]func(*Config){
		"language":   func(c *Config) { c.Language = "fr" },
		"fallback":   func(c *Config) { c.Fallback = "festival" },
		"ort":        func(c *Config) { c.ORTLibrary = "" },
		"provider":   func(c *Config) { c.Provider = "rocm" },
		"threads":    func(c *Config) { c.Threads = 0 },
		"cpu device": func(c *Config) { c.Device = 1 },
		"speed":      func(c *Config) { c.Speed = 3 },
		"mismatch":   func(c *Config) { c.Language, c.Voice = "gb", "af_heart" },
	} {
		c := ok
		c.Voice = ""
		mutate(&c)
		if err := c.Validate(true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	voices := []string{"af_alloy", "af_nova", "am_echo", "am_onyx", "bm_fable", "af_bella", "af_heart", "af_sarah", "af_jessica", "am_adam", "am_michael", "am_eric", "am_puck"}
	l, _ := Load("", env(nil), nil)
	if err := l.ValidateServer(voices); err != nil {
		t.Fatalf("default server: %v", err)
	}
	for name, mutate := range map[string]func(*Server){
		"listen":   func(s *Server) { s.Listen = "8880" },
		"replicas": func(s *Server) { s.Replicas = 0 },
		"queue":    func(s *Server) { s.MaxQueue = -1 },
		"speed":    func(s *Server) { s.Speed = SpeedRange{1.5, 1} },
		"chars":    func(s *Server) { s.Limits.MaxInputChars = 0 },
		"timeout":  func(s *Server) { s.Limits.RequestTimeout = 0 },
		"log":      func(s *Server) { s.LogFormat = "xml" },
		"key":      func(s *Server) { s.APIKey = "  " },
		"alias":    func(s *Server) { s.ModelAliases = map[string]string{"tts-1": "other"} },
		"voice":    func(s *Server) { s.Voices = map[string]string{"alloy": "zf_xiaobei"} },
	} {
		c := l.Config
		c.Server.ModelAliases, c.Server.Voices = DefaultModelAliases(), DefaultVoices()
		mutate(&c.Server)
		if err := c.ValidateServer(voices); err == nil {
			t.Errorf("server %s: accepted", name)
		}
	}
}

func TestKeysAndEnvNames(t *testing.T) {
	keys := Keys()
	for _, want := range []string{"ort_library", "assets", "server.listen", "server.limits.request_timeout", "server.speed.min", "server.apikey"} {
		found := false
		for _, k := range keys {
			found = found || k == want
		}
		if !found {
			t.Errorf("Keys() lacks %s: %v", want, keys)
		}
	}
	if got := EnvName("server.limits.max_input_chars"); got != "KOKORO_RUN_SERVER_LIMITS_MAX_INPUT_CHARS" {
		t.Errorf("EnvName = %s", got)
	}
	// The variables kokoro-run used before the config file keep working.
	if EnvName("assets") != "KOKORO_RUN_ASSETS" || EnvName("ort_library") != "KOKORO_RUN_ORT_LIBRARY" {
		t.Error("legacy variable names changed")
	}
}
