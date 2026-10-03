package config

import (
	"testing"
)

// TestDockerConfigs loads the images' configs, so they can't drift from the
// settings: an unknown or misspelled key fails here instead of in a container.
func TestDockerConfigs(t *testing.T) {
	voices := make([]string, 0, len(DefaultVoices()))
	for _, v := range DefaultVoices() {
		voices = append(voices, v)
	}
	for file, provider := range map[string]string{"../../docker/config.yaml": "cpu", "../../docker/config.cuda.yaml": "cuda", "../../docker/jetson-orin/config.yaml": "cuda"} {
		l, err := Load(file, func(string) string { return "" }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Validate(true); err != nil {
			t.Errorf("%s: %v", file, err)
		}
		if err := l.ValidateServer(voices); err != nil {
			t.Errorf("%s: %v", file, err)
		}
		if l.Provider != provider || l.Server.Listen != ":8880" || l.Model != "/var/lib/kokoro-run/assets/kokoro/kokoro-v1.0.onnx" {
			t.Errorf("%s: provider %s, listen %s, model %s", file, l.Provider, l.Server.Listen, l.Model)
		}
		// Every setting is written out, so the file documents them all.
		for _, key := range Keys() {
			if key == "model" || key == "voices_file" || key == "vocab" || key == "data" || key == "frontend_models" ||
				key == "espeak" || key == "verbose" || key == "language" || key == "device" || key == "server.apikey" || key == "server.api_key_file" ||
				key == "server.model_aliases" || key == "server.voices" {
				continue // derived, rarely changed or secret; covered by the comments and README
			}
			if l.Sources[key] != FromFile {
				t.Errorf("%s does not set %s", file, key)
			}
		}
	}
}
