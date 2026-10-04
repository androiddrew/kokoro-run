package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	g2p "github.com/androiddrew/go-g2p"
	"github.com/hajimehoshi/go-mp3"

	settings "github.com/androiddrew/kokoro-run/internal/config"
	"github.com/androiddrew/kokoro-run/internal/engine"
	"github.com/androiddrew/kokoro-run/internal/server"
)

var testVoices = []string{"af_alloy", "af_bella", "af_heart", "af_jessica", "af_nova", "af_sarah", "am_adam", "am_echo", "am_eric", "am_michael", "am_onyx", "am_puck", "bf_emma", "bm_fable", "zf_xiaobei"}

// fake records requests and streams fixed chunks.
type fake struct {
	mu       sync.Mutex
	requests []engine.Request
	chunks   []engine.Chunk
	err      error         // yielded after the chunks
	hold     chan struct{} // if set, the stream waits on it (or the context) before its first chunk
	started  chan struct{} // if set, receives once per stream as it starts
}

func (f *fake) Stream(ctx context.Context, r engine.Request) iter.Seq2[engine.Chunk, error] {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	chunks, err, hold := f.chunks, f.err, f.hold
	f.mu.Unlock()
	return func(yield func(engine.Chunk, error) bool) {
		if f.started != nil {
			f.started <- struct{}{}
		}
		if hold != nil {
			select {
			case <-hold:
			case <-ctx.Done():
				yield(engine.Chunk{}, ctx.Err())
				return
			}
		}
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
		if err != nil {
			yield(engine.Chunk{}, err)
		}
	}
}

func (f *fake) last(t *testing.T) engine.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("no request reached the engine")
	}
	return f.requests[len(f.requests)-1]
}

func pcm(n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = 0.25
	}
	return s
}

func newConfig(t *testing.T, yaml string, env map[string]string) *settings.Config {
	t.Helper()
	path := ""
	if yaml != "" {
		path = t.TempDir() + "/config.yaml"
		if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := settings.Load(path, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.ValidateServer(testVoices); err != nil {
		t.Fatal(err)
	}
	l.Server.FFmpeg = "" // the tests don't depend on an installed ffmpeg
	return &l.Config
}

func newServer(t *testing.T, yaml string, env map[string]string, synths ...server.Synthesizer) (http.Handler, *server.Pool) {
	t.Helper()
	cfg := newConfig(t, yaml, env)
	pool := server.NewPool(cfg.Server.Replicas, cfg.Server.MaxQueue)
	for _, s := range synths {
		pool.Add(s)
	}
	return server.New(cfg, pool, testVoices), pool
}

func speech(h http.Handler, body string, header ...string) *httptest.ResponseRecorder {
	return speechCtx(context.Background(), h, body, header...)
}

func speechCtx(ctx context.Context, h http.Handler, body string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if v := recover(); v != nil && v != http.ErrAbortHandler {
				panic(v)
			}
		}()
		h.ServeHTTP(w, r)
	}()
	return w
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func openAIError(t *testing.T, w *httptest.ResponseRecorder, status int, param string) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
	var body struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Param   *string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Message == "" || body.Error.Type == "" {
		t.Fatalf("not an OpenAI error: %s", w.Body)
	}
	if got := ""; body.Error.Param != nil {
		got = *body.Error.Param
		if got != param {
			t.Errorf("param %q, want %q", got, param)
		}
	} else if param != "" {
		t.Errorf("param null, want %q", param)
	}
	return body.Error.Message
}

func TestFormats(t *testing.T) {
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(2400), Tokens: 5}, {PCM: pcm(1200), Tokens: 3}}}
	h, _ := newServer(t, "", nil, f)

	w := speech(h, `{"model":"tts-1","input":"Hi.","voice":"alloy","response_format":"pcm"}`)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/pcm" || w.Body.Len() != 2*3600 {
		t.Fatalf("pcm: %d %s %d bytes", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}
	if got := int16(binary.LittleEndian.Uint16(w.Body.Bytes())); got < 8191 || got > 8192 {
		t.Errorf("first pcm sample %d", got)
	}

	w = speech(h, `{"model":"tts-1","input":"Hi.","voice":"alloy","response_format":"wav"}`)
	b := w.Body.Bytes()
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(b, []byte("RIFF")) || len(b) != 44+2*3600 {
		t.Fatalf("wav: %d %s %d bytes", w.Code, w.Header().Get("Content-Type"), len(b))
	}
	if rate := binary.LittleEndian.Uint32(b[24:]); rate != engine.SampleRate {
		t.Errorf("wav rate %d", rate)
	}

	w = speech(h, `{"model":"tts-1","input":"Hi.","voice":"alloy"}`)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("default mp3: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	dec, err := mp3.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	if err != nil || dec.SampleRate() != engine.SampleRate {
		t.Fatalf("mp3 decode: %v", err)
	}

	for _, format := range []string{"opus", "aac", "flac"} {
		msg := openAIError(t, speech(h, `{"model":"tts-1","input":"Hi.","voice":"alloy","response_format":"`+format+`"}`), 400, "response_format")
		if !strings.Contains(msg, "needs ffmpeg") {
			t.Errorf("%s without ffmpeg: %s", format, msg)
		}
	}
	openAIError(t, speech(h, `{"model":"tts-1","input":"Hi.","voice":"alloy","response_format":"ogg"}`), 400, "response_format")
}

func TestRequestReachesTheEngine(t *testing.T) {
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}}
	h, _ := newServer(t, "trim: false\nnormalize: false\nserver:\n  chunk_gap: 80ms\n  strict_pronunciation: true\n  speed: {min: 0.8, max: 1.5}\n", nil, f)
	for _, c := range []struct {
		body              string
		voice             string
		speed             float32
		normalize, markdn bool
	}{
		{`{"model":"tts-1","input":"x","voice":"nova"}`, "af_nova", 1, false, true},
		{`{"model":"kokoro-v1.0","input":"x","voice":{"id":"bf_emma"},"speed":4}`, "bf_emma", 1.5, false, true},
		{`{"model":"gpt-4o-mini-tts","input":"x","voice":"am_adam","speed":0.25,"normalize":true,"markdown":false}`, "am_adam", 0.8, true, false},
	} {
		if w := speech(h, c.body, "Content-Type", "application/json"); w.Code != 200 {
			t.Fatalf("%s: %d %s", c.body, w.Code, w.Body)
		}
		r := f.last(t)
		if r.Text != "x" || r.Voice != c.voice || r.Speed != c.speed || r.Prep.Normalize != c.normalize || r.Prep.Markdown != c.markdn {
			t.Errorf("%s: engine got %+v", c.body, r)
		}
		if r.Trim || r.Gap != 80*time.Millisecond || !r.Strict {
			t.Errorf("config not applied: %+v", r)
		}
	}
}

func TestValidation(t *testing.T) {
	h, _ := newServer(t, "server:\n  limits: {max_input_chars: 10}\n", nil, &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}})
	for _, c := range []struct{ body, param string }{
		{`{"model":"tts-2","input":"x","voice":"alloy"}`, "model"},
		{`{"input":"x","voice":"alloy"}`, "model"},
		{`{"model":"tts-1","voice":"alloy"}`, "input"},
		{`{"model":"tts-1","input":"","voice":"alloy"}`, "input"},
		{`{"model":"tts-1","input":"01234567890","voice":"alloy"}`, "input"},
		{`{"model":"tts-1","input":"x","voice":"zf_xiaobei"}`, "voice"},
		{`{"model":"tts-1","input":"x","voice":"robot"}`, "voice"},
		{`{"model":"tts-1","input":"x"}`, "voice"},
		{`{"model":"tts-1","input":"x","voice":"alloy","speed":4.5}`, "speed"},
		{`{"model":"tts-1","input":"x","voice":"alloy","speed":0.1}`, "speed"},
		{`{"model":"tts-1","input":"x","voice":"alloy","stream_format":"ws"}`, "stream_format"},
		{`{"model":"tts-1","input":"x","voice":"alloy"} {}`, ""},
		{`not json`, ""},
	} {
		openAIError(t, speech(h, c.body), 400, c.param)
	}
	big := `{"model":"tts-1","voice":"alloy","input":"` + strings.Repeat("x", 100000) + `"}`
	openAIError(t, speech(h, big), 413, "")
}

func TestSSE(t *testing.T) {
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(100), Tokens: 4}, {PCM: pcm(50), Tokens: 2}}}
	h, _ := newServer(t, "", nil, f)
	w := speech(h, `{"model":"tts-1","input":"Hi. Bye.","voice":"alloy","response_format":"pcm","stream_format":"sse"}`)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Content-Type"))
	}
	events := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	if len(events) != 3 {
		t.Fatalf("got %d events: %s", len(events), w.Body)
	}
	var audio int
	for _, e := range events[:2] {
		var delta struct{ Type, Audio string }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(e, "data: ")), &delta); err != nil || delta.Type != "speech.audio.delta" {
			t.Fatalf("delta %q: %v", e, err)
		}
		b, _ := base64.StdEncoding.DecodeString(delta.Audio)
		audio += len(b)
	}
	var done struct {
		Type  string
		Usage struct{ InputTokens, TotalTokens int } `json:"usage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(events[2], "data: ")), &done); err != nil {
		t.Fatal(err)
	}
	if audio != 300 || done.Type != "speech.audio.done" {
		t.Errorf("audio %d bytes, done %+v", audio, done)
	}
}

func TestEngineErrors(t *testing.T) {
	h, _ := newServer(t, "", nil, &fake{err: fmt.Errorf("%w: unresolved \"zxq\"", engine.ErrPronunciation)})
	msg := openAIError(t, speech(h, `{"model":"tts-1","input":"zxq","voice":"alloy"}`), 422, "input")
	if !strings.Contains(msg, "zxq") {
		t.Errorf("422 message: %s", msg)
	}
	h, _ = newServer(t, "", nil, &fake{err: errors.New("boom")})
	openAIError(t, speech(h, `{"model":"tts-1","input":"x","voice":"alloy"}`), 500, "")

	// After audio has gone out, a failure cuts the response off rather than
	// letting truncated audio pass for complete.
	h, _ = newServer(t, "", nil, &fake{chunks: []engine.Chunk{{PCM: pcm(100)}}, err: errors.New("boom")})
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/audio/speech", "application/json", strings.NewReader(`{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bytes.NewBuffer(nil).ReadFrom(resp.Body); err == nil {
		t.Fatal("a failed stream ended cleanly")
	}
}

func TestQueue(t *testing.T) {
	hold := make(chan struct{})
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}, hold: hold, started: make(chan struct{}, 10)}
	h, pool := newServer(t, "server:\n  max_queue: 1\n", nil, f)
	body := `{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`

	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- speech(h, body) }()
	<-f.started
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan *httptest.ResponseRecorder)
	go func() { queued <- speechCtx(ctx, h, body) }()
	waitFor(t, "a waiting request", func() bool { return pool.Waiting() == 1 })

	w := speech(h, body)
	openAIError(t, w, 429, "")
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}

	// A queued client that leaves frees its place.
	cancel()
	if w := <-queued; w.Code != 200 && w.Body.Len() != 0 {
		t.Errorf("departed client got %d %s", w.Code, w.Body)
	}
	waitFor(t, "the place to free", func() bool { return pool.Waiting() == 0 })
	second := make(chan *httptest.ResponseRecorder)
	go func() { second <- speech(h, body) }()
	waitFor(t, "the next request to wait", func() bool { return pool.Waiting() == 1 })

	close(hold)
	for _, c := range []chan *httptest.ResponseRecorder{first, second} {
		if w := <-c; w.Code != 200 {
			t.Errorf("queued request: %d %s", w.Code, w.Body)
		}
	}
}

func TestReplicasRunTogether(t *testing.T) {
	hold := make(chan struct{})
	a := &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}, hold: hold, started: make(chan struct{}, 1)}
	b := &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}, hold: hold, started: make(chan struct{}, 1)}
	h, _ := newServer(t, "server:\n  replicas: 2\n  max_queue: 0\n", nil, a, b)
	body := `{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`
	done := make(chan int, 2)
	for range 2 {
		go func() { done <- speech(h, body).Code }()
	}
	<-a.started
	<-b.started
	openAIError(t, speech(h, body), 429, "")
	close(hold)
	for range 2 {
		if code := <-done; code != 200 {
			t.Errorf("parallel request: %d", code)
		}
	}
}

func TestTimeout(t *testing.T) {
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}, hold: make(chan struct{})}
	h, _ := newServer(t, "server:\n  limits: {request_timeout: 50ms}\n", nil, f)
	msg := openAIError(t, speech(h, `{"model":"tts-1","input":"x","voice":"alloy"}`), 503, "")
	if !strings.Contains(msg, "timed out") {
		t.Errorf("timeout message: %s", msg)
	}
}

func TestLoadingAndReadiness(t *testing.T) {
	h, pool := newServer(t, "server:\n  replicas: 2\n  limits: {request_timeout: 5s}\n", nil)
	if w := get(h, "/readyz"); w.Code != 503 || !strings.Contains(w.Body.String(), "loading") {
		t.Fatalf("readyz while loading: %d %s", w.Code, w.Body)
	}
	if w := get(h, "/healthz"); w.Code != 200 {
		t.Fatalf("healthz: %d", w.Code)
	}
	// A request waits for the first replica to load.
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- speech(h, `{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`) }()
	waitFor(t, "the request to wait", func() bool { return pool.Waiting() == 1 })
	pool.Fail(errors.New("out of memory"))
	pool.Add(&fake{chunks: []engine.Chunk{{PCM: pcm(10)}}})
	if w := <-done; w.Code != 200 {
		t.Fatalf("request after load: %d %s", w.Code, w.Body)
	}
	if w := get(h, "/readyz"); w.Code != 200 || !strings.Contains(w.Body.String(), `"loaded":1`) {
		t.Fatalf("readyz after load: %d %s", w.Code, w.Body)
	}

	h, pool = newServer(t, "", nil)
	pool.Fail(errors.New("model file missing"))
	msg := openAIError(t, speech(h, `{"model":"tts-1","input":"x","voice":"alloy"}`), 503, "")
	if !strings.Contains(msg, "model file missing") {
		t.Errorf("unavailable message: %s", msg)
	}
	if w := get(h, "/readyz"); w.Code != 503 || !strings.Contains(w.Body.String(), "failed") {
		t.Fatalf("readyz after failure: %d %s", w.Code, w.Body)
	}
}

func TestAuth(t *testing.T) {
	h, _ := newServer(t, "", map[string]string{"KOKORO_RUN_API_KEY": "sekrit"}, &fake{chunks: []engine.Chunk{{PCM: pcm(10)}}})
	body := `{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`
	for _, header := range [][]string{nil, {"Authorization", "Bearer wrong"}, {"Authorization", "Basic sekrit"}} {
		w := speech(h, body, header...)
		openAIError(t, w, 401, "")
		if w.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Error("401 without WWW-Authenticate")
		}
	}
	if w := speech(h, body, "Authorization", "bearer sekrit"); w.Code != 200 {
		t.Fatalf("right key: %d", w.Code)
	}
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		if w := get(h, path); w.Code != 200 {
			t.Errorf("%s with auth on: %d", path, w.Code)
		}
	}
}

func TestModelsAndVoices(t *testing.T) {
	h, _ := newServer(t, "", nil)
	var models struct {
		Object string
		Data   []struct {
			ID       string `json:"id"`
			AliasFor string `json:"alias_for"`
		}
	}
	if err := json.Unmarshal(get(h, "/v1/models").Body.Bytes(), &models); err != nil || models.Object != "list" || len(models.Data) != 4 || models.Data[0].ID != settings.ModelName || models.Data[1].AliasFor != settings.ModelName {
		t.Fatalf("models: %+v %v", models, err)
	}
	var voices struct {
		Voices []struct{ Name, Language string }
		OpenAI map[string]string
	}
	if err := json.Unmarshal(get(h, "/v1/voices").Body.Bytes(), &voices); err != nil {
		t.Fatal(err)
	}
	if len(voices.Voices) != len(testVoices)-1 || voices.OpenAI["alloy"] != "af_alloy" {
		t.Fatalf("voices: %+v", voices)
	}
	for _, v := range voices.Voices {
		if v.Name == "zf_xiaobei" || (v.Language != "us" && v.Language != "gb") {
			t.Errorf("voice %+v", v)
		}
	}
}

func TestMetrics(t *testing.T) {
	f := &fake{chunks: []engine.Chunk{{PCM: pcm(2400), Diagnostics: []g2p.Diagnostic{{Code: "generation_limit", Text: "zxq"}}}}}
	h, _ := newServer(t, "", nil, f)
	speech(h, `{"model":"tts-1","input":"x","voice":"alloy","response_format":"pcm"}`)
	speech(h, `{"model":"tts-1","input":"x","voice":"robot"}`)
	m := get(h, "/metrics").Body.String()
	for _, want := range []string{
		`kokoro_requests_total{format="pcm",model="kokoro-v1.0",status="200"} 1`,
		`kokoro_requests_total{format="mp3",model="kokoro-v1.0",status="400"} 1`,
		`kokoro_replicas_loaded 1`,
		`kokoro_queue_depth 0`,
		`kokoro_rtf_count{model="kokoro-v1.0"} 1`,
		`kokoro_time_to_first_audio_seconds_count{model="kokoro-v1.0"} 1`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	h, _ = newServer(t, "server:\n  metrics: false\n", nil)
	if w := get(h, "/metrics"); w.Code != 404 {
		t.Errorf("metrics off: %d", w.Code)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
