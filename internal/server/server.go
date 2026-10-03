// Package server is the OpenAI-compatible HTTP API, adapted from gokittentts.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	g2p "github.com/androiddrew/go-g2p"

	"github.com/androiddrew/kokoro-run/internal/audio"
	settings "github.com/androiddrew/kokoro-run/internal/config"
	"github.com/androiddrew/kokoro-run/internal/engine"
	"github.com/androiddrew/kokoro-run/internal/textprep"
)

// OpenAI accepts speeds in this range; anything else is a 400. Accepted
// speeds are clamped to server.speed.
const (
	minSpeed = 0.25
	maxSpeed = 4.0
)

// format is a response format.
type format struct {
	contentType string
	ffmpeg      bool // encoded by piping through ffmpeg
}

var formats = map[string]format{
	"mp3":  {"audio/mpeg", false},
	"wav":  {"audio/wav", false},
	"pcm":  {"audio/pcm", false},
	"opus": {"audio/ogg", true},
	"aac":  {"audio/aac", true},
	"flac": {"audio/flac", true},
}

// defaultFormat is OpenAI's default.
const defaultFormat = "mp3"

type server struct {
	cfg     *settings.Config
	pool    *Pool
	voices  []string // the archive's English voices, sorted
	ffmpeg  string   // resolved path, or empty when ffmpeg is disabled or missing
	metrics *metrics // nil when metrics are off
}

// New returns the HTTP handler for cfg, synthesizing with pool's replicas
// and offering voices, the archive's English voices. If cfg's ffmpeg can't be
// found, opus, aac and flac are rejected. With an API key set, /v1/*
// requires it; /healthz, /readyz and /metrics are always open.
func New(cfg *settings.Config, pool *Pool, voices []string) http.Handler {
	s := &server{cfg: cfg, pool: pool}
	for _, v := range voices {
		if _, ok := settings.VoiceLanguage(v); ok {
			s.voices = append(s.voices, v)
		}
	}
	slices.Sort(s.voices)
	if cfg.Server.APIKey == "" && !isLoopback(cfg.Server.Listen) {
		slog.Warn("auth is off and the server listens beyond loopback; set KOKORO_RUN_API_KEY to require a key", "listen", cfg.Server.Listen)
	}
	if cfg.Server.FFmpeg != "" {
		// LookPath can return a path alongside exec.ErrDot; that path is not used.
		if path, err := exec.LookPath(cfg.Server.FFmpeg); err != nil {
			slog.Warn("ffmpeg not found; opus, aac and flac are disabled", "ffmpeg", cfg.Server.FFmpeg, "err", err)
		} else {
			s.ffmpeg = path
		}
	}
	if cfg.Server.Metrics {
		s.metrics = newMetrics(pool, s.supportedFormats())
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/audio/speech", s.speech)
	api.HandleFunc("GET /v1/voices", s.listVoices)
	api.HandleFunc("GET /v1/models", s.models)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.requireKey(api))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.ready)
	if s.metrics != nil {
		mux.Handle("GET /metrics", s.metrics.handler)
	}
	return mux
}

// ready answers 200 once a replica is loaded, and 503 before then or if
// none could be.
func (s *server) ready(w http.ResponseWriter, _ *http.Request) {
	loaded := s.pool.Loaded()
	body := map[string]any{"replicas": s.cfg.Server.Replicas, "loaded": loaded}
	if loaded == 0 {
		body["status"] = "loading"
		select {
		case <-s.pool.dead:
			body["status"] = "failed"
		default:
		}
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	body["status"] = "ready"
	writeJSON(w, http.StatusOK, body)
}

// requireKey wraps next to require "Authorization: Bearer <key>" when an
// API key is configured. Keys are compared as SHA-256 digests in constant
// time, so neither their contents nor their length leaks.
func (s *server) requireKey(next http.Handler) http.Handler {
	if s.cfg.Server.APIKey == "" {
		return next
	}
	want := sha256.Sum256([]byte(s.cfg.Server.APIKey))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, key, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		got := sha256.Sum256([]byte(key))
		if !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, invalidRequest, "", "missing or invalid API key; send Authorization: Bearer <key>")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether a listen address only accepts local
// connections. An empty host listens on every interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// speechRequest is OpenAI's CreateSpeechRequest plus normalize and markdown.
type speechRequest struct {
	Model          string          `json:"model"`
	Input          *string         `json:"input"`
	Voice          json.RawMessage `json:"voice"` // a string, or an object with an id
	ResponseFormat string          `json:"response_format"`
	Speed          *float64        `json:"speed"`
	StreamFormat   string          `json:"stream_format"`
	Instructions   string          `json:"instructions"` // accepted and ignored
	Normalize      *bool           `json:"normalize"`
	Markdown       *bool           `json:"markdown"`
}

func (s *server) speech(w http.ResponseWriter, r *http.Request) {
	rec := &requestRecord{start: time.Now()}
	// Deferred calls also run when the response is aborted with a panic.
	defer s.finish(rec)

	var req speechRequest
	if status, msg := s.decode(w, r, &req); status != 0 {
		rec.fail(w, status, invalidRequest, "", msg)
		return
	}
	if req.Instructions != "" {
		slog.Debug("ignoring instructions", "instructions", req.Instructions)
	}
	format := req.ResponseFormat
	if format == "" {
		format = defaultFormat
	}
	rec.model, rec.format = req.Model, format
	rec.voice, _ = voiceName(req.Voice)
	if req.Input != nil {
		rec.inputChars = utf8.RuneCountInString(*req.Input)
	}
	if _, ok := formats[format]; ok {
		rec.formatLabel = format
	}

	if !s.knownModel(req.Model) {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "model",
			fmt.Sprintf("unknown model %q; the model is %s, with aliases %s",
				req.Model, settings.ModelName, strings.Join(sortedKeys(s.cfg.Server.ModelAliases), ", ")))
		return
	}
	rec.model, rec.modelLabel = settings.ModelName, settings.ModelName

	if req.Input == nil || *req.Input == "" {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "input", "input is required")
		return
	}
	if rec.inputChars > s.cfg.Server.Limits.MaxInputChars {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "input",
			fmt.Sprintf("input is %d characters; the limit is %d", rec.inputChars, s.cfg.Server.Limits.MaxInputChars))
		return
	}
	if !utf8.ValidString(*req.Input) || strings.ContainsRune(*req.Input, 0) {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "input", "input must be valid UTF-8 without NUL")
		return
	}

	voice, ok := s.resolveVoice(req.Voice)
	if !ok {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "voice", s.unknownVoiceMessage(req.Voice))
		return
	}
	rec.voice = voice

	f, ok := formats[format]
	if !ok || f.ffmpeg && s.ffmpeg == "" {
		reason := "is not supported"
		if ok {
			reason = "needs ffmpeg, which is not available"
		}
		rec.fail(w, http.StatusBadRequest, invalidRequest, "response_format",
			fmt.Sprintf("response_format %q %s; supported formats are %s", format, reason, strings.Join(s.supportedFormats(), ", ")))
		return
	}

	speed := 1.0
	if req.Speed != nil {
		speed = *req.Speed
	}
	if !(speed >= minSpeed && speed <= maxSpeed) {
		rec.fail(w, http.StatusBadRequest, invalidRequest, "speed",
			fmt.Sprintf("speed %v is outside %v to %v", speed, minSpeed, maxSpeed))
		return
	}

	switch req.StreamFormat {
	case "", "audio", "sse":
	default:
		rec.fail(w, http.StatusBadRequest, invalidRequest, "stream_format",
			fmt.Sprintf("stream_format %q is not supported; use audio or sse", req.StreamFormat))
		return
	}

	// The timeout covers the queue wait and synthesis.
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Server.Limits.RequestTimeout)
	defer cancel()
	queued := time.Now()
	synth, release, err := s.pool.enter(ctx)
	rec.queueWait = time.Since(queued)
	if errors.Is(err, errQueueFull) {
		w.Header().Set("Retry-After", "1")
		rec.fail(w, http.StatusTooManyRequests, rateLimit, "",
			fmt.Sprintf("all %d replicas are busy and the queue is full; retry shortly", s.cfg.Server.Replicas))
		return
	}
	if errors.Is(err, ErrUnavailable) {
		rec.fail(w, http.StatusServiceUnavailable, serverError, "", err.Error())
		return
	}
	if err != nil {
		s.writeContextError(w, r, rec, err)
		return
	}
	defer release()

	// Pull the first chunk before sending headers, so a failure that comes
	// before any audio is still an error response.
	pull, stop := iter.Pull2(synth.Stream(ctx, engine.Request{
		Text:   *req.Input,
		Voice:  voice,
		Speed:  max(s.cfg.Server.Speed.Min, min(s.cfg.Server.Speed.Max, float32(speed))),
		Prep:   textprep.Options{Normalize: orDefault(req.Normalize, s.cfg.Normalize), Markdown: orDefault(req.Markdown, s.cfg.Markdown)},
		Trim:   s.cfg.Trim,
		Gap:    s.cfg.Server.ChunkGap,
		Strict: s.cfg.Server.StrictPronunciation,
	}))
	defer stop()
	next := func() (engine.Chunk, error, bool) {
		start := time.Now()
		defer func() { rec.synthesis += time.Since(start) }()
		c, err, more := pull()
		if err == nil && len(c.Diagnostics) > 0 {
			rec.diagnostics = append(rec.diagnostics, c.Diagnostics...)
		}
		return c, err, more
	}
	chunk, err, more := next()
	if ctx.Err() != nil {
		s.writeContextError(w, r, rec, ctx.Err())
		return
	}
	if errors.Is(err, engine.ErrPronunciation) {
		rec.fail(w, http.StatusUnprocessableEntity, invalidRequest, "input", err.Error()+"; spell these words out or add pronunciation overrides")
		return
	}
	if err != nil {
		rec.fail(w, http.StatusInternalServerError, serverError, "", "synthesis failed: "+err.Error())
		return
	}

	sink := &flushWriter{w: w, rc: http.NewResponseController(w), first: func() { rec.firstAudio = time.Since(rec.start) }}
	var out io.Writer = sink
	var sse *sseWriter
	if req.StreamFormat == "sse" {
		sse = &sseWriter{sink}
		out = sse
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Content-Type", f.contentType)
	}
	// The headers go first: ffmpeg's output is copied to the response from
	// another goroutine as soon as it starts.
	w.WriteHeader(http.StatusOK)
	rec.status = http.StatusOK
	sink.rc.Flush()
	enc, err := s.encoder(ctx, out, format)
	if err != nil {
		rec.abort(r, fmt.Errorf("starting encoder: %w", err))
		return
	}

	var total usage
	for ; more; chunk, err, more = next() {
		if err == nil {
			total.InputTokens += chunk.Tokens
			err = enc.Write(chunk.PCM)
		}
		if err == nil {
			rec.chunks++
			rec.samples += len(chunk.PCM)
			// A disconnect or the timeout ends the context; don't start another run.
			err = ctx.Err()
		}
		if err != nil {
			// Drop the encoder's tail, but still reap it.
			sink.discard.Store(true)
			enc.Close()
			rec.abort(r, err)
			return
		}
	}
	if err := enc.Close(); err != nil {
		rec.abort(r, fmt.Errorf("encoding: %w", err))
		return
	}
	if sse != nil {
		total.TotalTokens = total.InputTokens + total.OutputTokens
		sse.event(map[string]any{"type": "speech.audio.done", "usage": total})
	}
}

func orDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// writeContextError answers a request whose context ended before any audio
// was sent: nothing for a client that has gone, 503 for the timeout.
func (s *server) writeContextError(w http.ResponseWriter, r *http.Request, rec *requestRecord, err error) {
	if r.Context().Err() != nil {
		rec.status, rec.err = statusClientClosed, "client disconnected: "+err.Error()
		return
	}
	rec.fail(w, http.StatusServiceUnavailable, serverError, "",
		fmt.Sprintf("request timed out after %v waiting for or running the model", s.cfg.Server.Limits.RequestTimeout))
}

// statusClientClosed is the status logged and counted for a client that
// disconnected before its response ended (nginx's 499).
const statusClientClosed = 499

// requestRecord is what a speech request logs and counts when it ends.
type requestRecord struct {
	start                   time.Time
	model, voice, format    string // resolved, or as requested if they don't resolve
	modelLabel, formatLabel string // metric labels: empty unless known or supported
	inputChars              int
	chunks, samples         int           // audio written to the response
	synthesis               time.Duration // spent in the engine
	firstAudio              time.Duration // from the start to the first audio byte sent
	queueWait               time.Duration
	diagnostics             []g2p.Diagnostic // pronunciations spoken as far as known
	status                  int              // the HTTP status, or statusClientClosed; see abort
	err                     string           // why the request failed
}

// fail records and writes an error response.
func (rec *requestRecord) fail(w http.ResponseWriter, status int, typ, param, message string) {
	rec.status, rec.err = status, message
	writeError(w, status, typ, param, message)
}

// abort records why a started stream failed and, unless the client has
// gone, cuts the response off so it can't pass for complete audio. The
// response was 200, but it is recorded as 499 for a client that
// disconnected, 503 for the timeout and 500 for anything else.
func (rec *requestRecord) abort(r *http.Request, err error) {
	rec.err = err.Error()
	if r.Context().Err() != nil {
		rec.status = statusClientClosed
		return
	}
	rec.status = http.StatusInternalServerError
	if errors.Is(err, context.DeadlineExceeded) {
		rec.status = http.StatusServiceUnavailable
	}
	panic(http.ErrAbortHandler)
}

func (rec *requestRecord) audioSeconds() float64 {
	return float64(rec.samples) / engine.SampleRate
}

// rtf is the real-time factor: synthesis seconds per second of audio, or 0
// without audio.
func (rec *requestRecord) rtf() float64 {
	if rec.samples == 0 {
		return 0
	}
	return rec.synthesis.Seconds() / rec.audioSeconds()
}

// finish writes the request's log line and counts it.
func (s *server) finish(rec *requestRecord) {
	attrs := []any{
		"model", rec.model,
		"voice", rec.voice,
		"format", rec.format,
		"input_chars", rec.inputChars,
		"chunks", rec.chunks,
		"audio_seconds", rec.audioSeconds(),
		"synthesis_seconds", rec.synthesis.Seconds(),
		"rtf", rec.rtf(),
		"time_to_first_audio_seconds", rec.firstAudio.Seconds(),
		"queue_wait_seconds", rec.queueWait.Seconds(),
		"duration_seconds", time.Since(rec.start).Seconds(),
		"status", rec.status,
	}
	level := slog.LevelInfo
	if len(rec.diagnostics) > 0 {
		// The text was spoken without these sounds.
		attrs = append(attrs, "pronunciation_diagnostics", rec.diagnostics)
		level = slog.LevelWarn
	}
	if rec.err != "" {
		attrs = append(attrs, "error", rec.err)
	}
	if rec.status >= 500 {
		level = slog.LevelError
	}
	slog.Log(context.Background(), level, "speech", attrs...)
	if s.metrics != nil {
		s.metrics.observe(rec)
	}
}

// encoder returns the Encoder for format, writing to w.
func (s *server) encoder(ctx context.Context, w io.Writer, format string) (audio.Encoder, error) {
	switch format {
	case "mp3":
		return audio.NewMP3Encoder(w, engine.SampleRate)
	case "wav":
		return audio.NewWAVStreamEncoder(w, engine.SampleRate), nil
	case "pcm":
		return audio.NewPCMEncoder(w), nil
	}
	if formats[format].ffmpeg {
		return audio.NewFFmpegEncoder(ctx, w, s.ffmpeg, engine.SampleRate, format)
	}
	return nil, fmt.Errorf("no encoder for %q", format)
}

// usage is the speech.audio.done usage: input tokens are the phoneme tokens
// the model ran on. Kokoro has no output tokens to count.
type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// flushWriter flushes the response after every Write. Once discard is set,
// it drops writes but reports success, so an encoder can still drain. first,
// if set, is called before the first Write.
type flushWriter struct {
	w       io.Writer
	rc      *http.ResponseController
	discard atomic.Bool
	first   func()
}

func (f *flushWriter) Write(p []byte) (int, error) {
	if f.discard.Load() {
		return len(p), nil
	}
	if f.first != nil {
		f.first()
		f.first = nil
	}
	n, err := f.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.rc.Flush()
}

// sseWriter sends each Write as a speech.audio.delta event.
type sseWriter struct{ w *flushWriter }

func (s *sseWriter) Write(p []byte) (int, error) {
	if err := s.event(map[string]string{"type": "speech.audio.delta", "audio": base64.StdEncoding.EncodeToString(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *sseWriter) event(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.w.Write(fmt.Appendf(nil, "data: %s\n\n", b))
	return err
}

// supportedFormats lists the response formats this server can produce.
func (s *server) supportedFormats() []string {
	var names []string
	for _, name := range sortedKeys(formats) {
		if !formats[name].ffmpeg || s.ffmpeg != "" {
			names = append(names, name)
		}
	}
	return names
}

// decode reads exactly one JSON object from a body capped at a size that
// always fits max_input_chars characters, even when every one is escaped.
// It returns a non-zero status and a message on failure.
func (s *server) decode(w http.ResponseWriter, r *http.Request, req *speechRequest) (int, string) {
	limit := int64(64<<10 + 12*s.cfg.Server.Limits.MaxInputChars) // \uXXXX\uXXXX is 12 bytes
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	err := dec.Decode(req)
	if err == nil {
		if err = dec.Decode(&struct{}{}); err == io.EOF {
			return 0, ""
		} else if err == nil {
			err = errors.New("more than one JSON value")
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge, fmt.Sprintf("request body is larger than %d bytes", limit)
	}
	return http.StatusBadRequest, "invalid JSON body: " + err.Error()
}

// knownModel reports whether name is the model or one of its aliases.
func (s *server) knownModel(name string) bool {
	if name == settings.ModelName {
		return true
	}
	_, ok := s.cfg.Server.ModelAliases[name]
	return ok
}

// resolveVoice resolves a voice, given as a string or an object with an id,
// as a Kokoro voice, then an OpenAI voice from the config's map.
func (s *server) resolveVoice(raw json.RawMessage) (string, bool) {
	name, ok := voiceName(raw)
	if !ok {
		return "", false
	}
	if _, found := slices.BinarySearch(s.voices, name); found {
		return name, true
	}
	voice, ok := s.cfg.Server.Voices[name]
	return voice, ok
}

func voiceName(raw json.RawMessage) (string, bool) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name, true
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.ID != "" {
		return obj.ID, true
	}
	return "", false
}

func (s *server) unknownVoiceMessage(raw json.RawMessage) string {
	given := "missing voice"
	if len(raw) > 0 {
		given = "unknown voice " + string(raw)
	}
	return fmt.Sprintf("%s; valid voices are %s, or %s", given, strings.Join(s.voices, ", "), strings.Join(sortedKeys(s.cfg.Server.Voices), ", "))
}

// modelInfo is an entry in OpenAI's model list, plus alias_for on aliases.
type modelInfo struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Created  int64  `json:"created"`
	OwnedBy  string `json:"owned_by"`
	AliasFor string `json:"alias_for,omitempty"`
}

// models lists the model, then the aliases.
func (s *server) models(w http.ResponseWriter, _ *http.Request) {
	list := []modelInfo{{ID: settings.ModelName, Object: "model", OwnedBy: "hexgrad"}}
	for _, alias := range sortedKeys(s.cfg.Server.ModelAliases) {
		list = append(list, modelInfo{ID: alias, Object: "model", OwnedBy: "hexgrad", AliasFor: settings.ModelName})
	}
	writeJSON(w, http.StatusOK, struct {
		Object string      `json:"object"`
		Data   []modelInfo `json:"data"`
	}{"list", list})
}

type voiceInfo struct {
	Name     string `json:"name"`
	Language string `json:"language"` // us or gb
}

func (s *server) listVoices(w http.ResponseWriter, _ *http.Request) {
	list := make([]voiceInfo, 0, len(s.voices))
	for _, v := range s.voices {
		lang, _ := settings.VoiceLanguage(v)
		list = append(list, voiceInfo{v, lang})
	}
	writeJSON(w, http.StatusOK, struct {
		Voices []voiceInfo       `json:"voices"`
		OpenAI map[string]string `json:"openai"`
	}{list, s.cfg.Server.Voices})
}

// OpenAI error types.
const (
	invalidRequest = "invalid_request_error"
	rateLimit      = "rate_limit_exceeded"
	serverError    = "server_error"
)

// writeError writes OpenAI's {"error": {message, type, param, code}}. An
// empty param is written as null; code is always null.
func writeError(w http.ResponseWriter, status int, typ, param, message string) {
	var p *string
	if param != "" {
		p = &param
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message,
		"type":    typ,
		"param":   p,
		"code":    nil,
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing response", "err", err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
