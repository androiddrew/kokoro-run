package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func execute(args []string, input string) (string, string, error) {
	cmd := NewCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func TestHelpAndValidation(t *testing.T) {
	help, _, err := execute([]string{"--help"}, "")
	if err != nil || !strings.Contains(help, "--fallback espeak") {
		t.Fatalf("%s: %v", help, err)
	}
	help, _, err = execute([]string{"synth", "--help"}, "")
	if err != nil || !strings.Contains(help, "--fallback") || !strings.Contains(help, "--espeak") {
		t.Fatalf("backend flags: %s: %v", help, err)
	}
	version, _, err := execute([]string{"--version"}, "")
	if err != nil || !strings.Contains(version, "devel") {
		t.Fatalf("version: %q %v", version, err)
	}
	for _, args := range [][]string{
		{"synth", "--text", "hello"},
		{"phonemize", "--text", "hello", "--file", "-"},
		{"voices", "--language", "xx"},
		{"synth", "--ort", "missing", "-o", "x", "--provider", "typo"},
		{"synth", "--ort", "missing", "-o", "x", "--speed", "NaN"},
		{"synth", "--ort", "missing", "-o", "x", "--threads", "0"},
		{"synth", "--ort", "missing", "-o", "x", "--language", "gb", "--voice", "af_heart"},
		{"synth", "--ort", "missing", "-o", "x", "--report", "./x"},
		{"bench", "--ort", "missing", "-o", "x", "--repeat", "0"},
		{"phonemize", "--ort", "missing", "--text", ""},
		{"phonemize", "--ort", "missing", "--text", "\xff"},
		{"phonemize", "--ort", "missing", "--text", "a\x00b"},
		{"phonemize", "--ort", "missing", "--text", strings.Repeat("a", 100001)},
		{"phonemize", "extra"},
		{"phonemize", "--ort", "missing", "--text", "hi", "--fallback", "festival"},
	} {
		if _, _, err := execute(args, ""); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func TestAssetBundleDefaults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KOKORO_RUN_ASSETS", root)
	cmd := NewCommand()
	synth, _, _ := cmd.Find([]string{"synth"})
	for flag, suffix := range map[string]string{
		"model": "kokoro/kokoro-v1.0.onnx", "voices": "kokoro/voices-v1.0.bin",
	} {
		got, err := synth.Flags().GetString(flag)
		if err != nil || got != filepath.Join(root, suffix) {
			t.Fatalf("%s: %q %v", flag, got, err)
		}
	}
	if err := synth.ParseFlags([]string{"--model", "/custom/model.onnx"}); err != nil {
		t.Fatal(err)
	}
	got, _ := synth.Flags().GetString("model")
	if got != "/custom/model.onnx" {
		t.Fatal("explicit model flag did not override bundle default")
	}
}

func TestTextSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("Unicode café"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		args        []string
		stdin, want string
	}{
		{[]string{"--text", "inline"}, "ignored", "inline"},
		{[]string{"--file", path}, "ignored", "Unicode café"},
		{[]string{"--file", "-"}, "piped", "piped"},
		{nil, "default", "default"},
	} {
		cmd := NewCommand()
		child, _, _ := cmd.Find([]string{"phonemize"})
		var input inputFlags
		// Read the real command's parsed flags, avoiding native initialization.
		if err := child.ParseFlags(source.args); err != nil {
			t.Fatal(err)
		}
		input.text, _ = child.Flags().GetString("text")
		input.file, _ = child.Flags().GetString("file")
		child.SetIn(strings.NewReader(source.stdin))
		got, err := readText(child, input)
		if err != nil || got != source.want {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	cmd := NewCommand()
	child, _, _ := cmd.Find([]string{"phonemize"})
	child.SetIn(strings.NewReader(strings.Repeat("x", 400001)))
	if _, err := readText(child, inputFlags{}); err == nil {
		t.Fatal("unbounded input accepted")
	}
}

func TestExclusiveOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	write := func(w io.Writer) error { _, err := io.WriteString(w, "retained"); return err }
	if err := writeNew(path, write); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(path, write); err == nil {
		t.Fatal("overwrote existing output")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "retained" {
		t.Fatal("modified existing file")
	}
	partial := path + "-partial"
	if err := writeNew(partial, func(w io.Writer) error { _, _ = io.WriteString(w, "partial"); return errors.New("failed") }); err == nil {
		t.Fatal("lost write error")
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("left partial output")
	}
}

func TestProviderEvidence(t *testing.T) {
	line := func(s string) string {
		return "2026 [V:onnxruntime:, session_state.cc:1260 VerifyEachNodeIsAssignedToAnEp] " + s + "\n"
	}
	header := line("Node placements")
	cpu := header + line(" All nodes placed on [CPUExecutionProvider]. Number of nodes: 2293")
	cuda := header + line(" Node(s) placed on [CPUExecutionProvider]. Number of nodes: 1") + line("  Shape (cpu)") + line(" Node(s) placed on [CUDAExecutionProvider]. Number of nodes: 2") + line("  Conv (gpu)") + line("  MatMul (gpu)")
	for _, test := range []struct {
		log, provider string
		valid         bool
	}{
		{cpu + cpu, "cpu", true}, {cuda + cuda, "cuda", true},
		{cpu + cpu, "cuda", false}, {cuda + cuda, "cpu", false}, {cuda, "cuda", false},
		{"CUDAExecutionProvider registered\n", "cuda", false},
		{strings.ReplaceAll(cuda+cuda, "Conv (gpu)", "MemcpyFromHost (gpu)"), "cuda", false},
		{strings.ReplaceAll(cuda+cuda, "nodes: 2", "nodes: 3"), "cuda", false},
		{strings.ReplaceAll(cuda+cuda, line("  Shape (cpu)"), ""), "cuda", false},
	} {
		_, err := parsePlacements(strings.NewReader(test.log), test.provider)
		if (err == nil) != test.valid {
			t.Fatalf("valid=%v provider=%s err=%v log=%s", test.valid, test.provider, err, test.log)
		}
	}
}

func TestRuntimeLibrarySelectors(t *testing.T) {
	for _, name := range []string{"libonnxruntime.so", "libonnxruntime.so.1", "onnxruntime.dll"} {
		if runtimeIsFilePath(name) {
			t.Fatalf("loader name treated as a local file: %q", name)
		}
	}
	for _, path := range []string{"./libonnxruntime.so", "../runtime/lib.so", "/usr/lib/libonnxruntime.so"} {
		if !runtimeIsFilePath(path) {
			t.Fatalf("explicit path not recognized: %q", path)
		}
	}
	metadata := buildMetadata()
	if _, exists := metadata["ort"]; exists {
		t.Fatal("build metadata must not claim an unloaded native runtime version")
	}
}
