package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	g2p "github.com/androiddrew/go-g2p"
	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/spf13/cobra"
	ort "github.com/yalue/onnxruntime_go"
)

type assetIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type placement struct {
	Nodes      int            `json:"nodes"`
	Operations map[string]int `json:"operations,omitempty"`
}

type doctorReport struct {
	Verified       bool                       `json:"verified"`
	Build          map[string]string          `json:"build"`
	Config         config                     `json:"config"`
	Dependencies   map[string]string          `json:"fallback_dependencies,omitempty"`
	Assets         []assetIdentity            `json:"assets,omitempty"`
	Model          kokoro.ModelInfo           `json:"model"`
	Frontend       map[g2p.Dialect]g2p.Result `json:"frontend,omitempty"`
	Audio          audioStats                 `json:"audio"`
	Placements     []map[string]*placement    `json:"kokoro_session_placements,omitempty"`
	LogSHA256      string                     `json:"ort_log_sha256,omitempty"`
	LogPath        string                     `json:"ort_log_path,omitempty"`
	Error          string                     `json:"error,omitempty"`
	RuntimeLibrary string                     `json:"runtime_library"`
	RuntimeVersion string                     `json:"runtime_version,omitempty"`
}

func identity(path string) (assetIdentity, error) {
	a := assetIdentity{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	h := sha256.New()
	a.Bytes, err = io.Copy(h, f)
	err = errors.Join(err, f.Close())
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	return a, err
}

// ORT logs at native fd 2, outside Cobra's writers. Use a separate process rather
// than redirecting process-global descriptors or racing other runtime owners.
func doctorCommand() *cobra.Command {
	var c config
	var logPath string
	cmd := &cobra.Command{Use: "doctor", Short: "Verify assets, both dialects, synthesis and actual provider placement", Args: cobra.NoArgs}
	frontendFlags(cmd, &c)
	synthesisFlags(cmd, &c)
	languageFlag(cmd, &c)
	cmd.Flags().StringVar(&logPath, "log", "", "Save native placement log to a new file (optional)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		if err = c.validate(true); err != nil {
			return err
		}
		report := doctorReport{Build: buildMetadata(), Config: c, LogPath: logPath, RuntimeLibrary: c.Frontend.ORTLibrary}
		var log *os.File
		if logPath == "" {
			log, err = os.CreateTemp("", "kokoro-run-doctor-*.log")
			if err == nil {
				defer os.Remove(log.Name())
			}
		} else {
			log, err = os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		}
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return errors.Join(err, log.Close())
		}
		data, err := json.Marshal(c)
		if err != nil {
			return errors.Join(err, log.Close())
		}
		child := exec.CommandContext(cmd.Context(), executable, "_doctor")
		child.Stdin = bytes.NewReader(data)
		var out bytes.Buffer
		child.Stdout, child.Stderr = &out, log
		runErr := errors.Join(child.Run(), log.Close())
		if runErr == nil {
			runErr = json.Unmarshal(out.Bytes(), &report)
		}
		report.LogPath = logPath
		id, idErr := identity(log.Name())
		report.LogSHA256 = id.SHA256
		if runErr == nil {
			f, e := os.Open(log.Name())
			if e == nil {
				report.Placements, e = parsePlacements(f, string(c.Synthesis.Provider))
				e = errors.Join(e, f.Close())
			}
			runErr = e
		} else {
			// Include the actionable native/worker error without flooding stdout with
			// thousands of node lines; --log retains the complete diagnostic evidence.
			f, e := os.Open(log.Name())
			if e == nil {
				if info, statErr := f.Stat(); statErr == nil && info.Size() > 4096 {
					_, _ = f.Seek(-4096, io.SeekEnd)
				}
				tail, _ := io.ReadAll(io.LimitReader(f, 4096))
				_ = f.Close()
				runErr = fmt.Errorf("diagnostic worker: %w: %s", runErr, strings.TrimSpace(string(tail)))
			}
		}
		runErr = errors.Join(runErr, idErr)
		report.Verified = runErr == nil
		if runErr != nil {
			report.Error = runErr.Error()
		}
		return errors.Join(runErr, encode(cmd.OutOrStdout(), report))
	}
	return cmd
}

func doctorWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "_doctor", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
		var c config
		decoder := json.NewDecoder(io.LimitReader(cmd.InOrStdin(), 1<<20))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&c); err != nil {
			return err
		}
		if err = c.validate(true); err != nil {
			return err
		}
		c.Synthesis.Verbose = true
		report := doctorReport{Build: buildMetadata(), Config: c, RuntimeLibrary: c.Frontend.ORTLibrary}
		report.Dependencies, err = backendDependencies(cmd, c)
		if err != nil {
			return err
		}
		paths := []string{c.Synthesis.ModelPath, c.Synthesis.VoicesPath}
		if c.Synthesis.VocabPath != "" {
			paths = append(paths, c.Synthesis.VocabPath)
		}
		// Explicit file paths can be fingerprinted. A loader name is resolved by
		// the OS, not relative to the working directory, so do not hash a guessed file.
		if runtimeIsFilePath(c.Frontend.ORTLibrary) {
			paths = append(paths, c.Frontend.ORTLibrary)
		}
		// Built-in frontend files are part of the binary; fingerprint only overrides.
		if c.Frontend.DataDir != "" {
			for _, name := range []string{"tokenizer.json", "unicode.json", "us_gold.json", "us_silver.json", "gb_gold.json", "gb_silver.json"} {
				paths = append(paths, filepath.Join(c.Frontend.DataDir, name))
			}
		}
		if c.Frontend.ModelDir != "" {
			for _, name := range append([]string{"pos.json", "pos.onnx"}, backendModelFiles(c)...) {
				paths = append(paths, filepath.Join(c.Frontend.ModelDir, name))
			}
		}
		for _, path := range paths {
			id, e := identity(path)
			if e != nil {
				return fmt.Errorf("required asset %s: %w; see CLI setup documentation", path, e)
			}
			report.Assets = append(report.Assets, id)
		}
		// Only Kokoro enables verbose logging, so the two placements below can be
		// unambiguously attributed to its metadata and synthesis sessions.
		synthesis, err := kokoro.New(c.Synthesis)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, synthesis.Close()) }()
		report.RuntimeVersion = ort.GetVersion()
		report.Model = synthesis.ModelInfo()
		frontend, err := newFrontend(c)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, frontend.close()) }()
		report.Frontend = map[g2p.Dialect]g2p.Result{}
		for _, dialect := range []g2p.Dialect{g2p.US, g2p.GB} {
			result, e := frontend.Phonemize(cmd.Context(), g2p.Request{Text: "Now outofdictionary words are handled by espeak.", Dialect: dialect})
			if e != nil {
				_ = diagnostics(cmd.ErrOrStderr(), result)
				return e
			}
			if !result.Complete || len(result.FallbackCalls) == 0 {
				return fmt.Errorf("%s fallback smoke request was not complete or did not exercise fallback", dialect)
			}
			report.Frontend[dialect] = result
		}
		result, err := synthesis.Synthesize(cmd.Context(), kokoro.Request{Phonemes: report.Frontend[g2p.Dialect(c.Language)].Phonemes, Voice: c.Voice, Speed: c.Speed, Trim: c.Trim})
		if err != nil {
			return err
		}
		report.Audio, err = statistics(result)
		if err != nil {
			return err
		}
		// Also check the production WAV encoder, without leaving a diagnostic WAV.
		if err = kokoro.WriteWAV(io.Discard, result.Samples); err != nil {
			return err
		}
		return encode(cmd.OutOrStdout(), report)
	}
	return cmd
}

func runtimeIsFilePath(library string) bool {
	return filepath.Base(library) != library || filepath.VolumeName(library) != ""
}

var providerLine = regexp.MustCompile(`placed on \[(\w+)\]\. Number of nodes: (\d+)`)
var operationLine = regexp.MustCompile(`^\s+(\w+) \(`)

func parsePlacements(r io.Reader, requested string) ([]map[string]*placement, error) {
	var sessions []map[string]*placement
	var current map[string]*placement
	var provider *placement
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.Index(line, "VerifyEachNodeIsAssignedToAnEp] "); i >= 0 {
			line = line[i+len("VerifyEachNodeIsAssignedToAnEp] "):]
		} else {
			continue
		}
		if line == "Node placements" {
			current = map[string]*placement{}
			sessions = append(sessions, current)
			provider = nil
		} else if m := providerLine.FindStringSubmatch(line); m != nil {
			if current == nil || current[m[1]] != nil {
				return nil, errors.New("invalid or duplicate provider placement")
			}
			n, err := strconv.Atoi(m[2])
			if err != nil {
				return nil, err
			}
			provider = &placement{Nodes: n, Operations: map[string]int{}}
			current[m[1]] = provider
		} else if m := operationLine.FindStringSubmatch(line); m != nil {
			if provider == nil {
				return nil, errors.New("operation without provider placement")
			}
			provider.Operations[m[1]]++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(sessions) != 2 {
		return nil, fmt.Errorf("expected two Kokoro session placements, got %d", len(sessions))
	}
	for _, session := range sessions {
		if requested == "cpu" {
			if len(session) != 1 || session["CPUExecutionProvider"] == nil || session["CPUExecutionProvider"].Nodes <= 0 {
				return nil, errors.New("CPU node placement not verified")
			}
		} else if requested == "cuda" {
			gpu := session["CUDAExecutionProvider"]
			if gpu == nil || gpu.Nodes <= 0 || gpu.Operations["Conv"] == 0 || gpu.Operations["MatMul"] == 0 {
				return nil, errors.New("CUDA computation not verified: require GPU Conv and MatMul, not registration or transfers alone")
			}
			for name, p := range session {
				if name != "CUDAExecutionProvider" && name != "CPUExecutionProvider" {
					return nil, fmt.Errorf("unexpected provider %s", name)
				}
				n := 0
				for _, count := range p.Operations {
					n += count
				}
				if n != p.Nodes {
					return nil, fmt.Errorf("incomplete %s node placement: %d operations, %d nodes", name, n, p.Nodes)
				}
			}
		} else {
			return nil, fmt.Errorf("unsupported provider %q", requested)
		}
	}
	return sessions, nil
}
