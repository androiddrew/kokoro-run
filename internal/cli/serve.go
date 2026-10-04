package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	kokoro "github.com/androiddrew/go-kokoro"
	"github.com/spf13/cobra"

	settings "github.com/androiddrew/kokoro-run/internal/config"
	"github.com/androiddrew/kokoro-run/internal/engine"
	"github.com/androiddrew/kokoro-run/internal/server"
)

func serveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the OpenAI-compatible speech API",
		Long: "Serve POST /v1/audio/speech, GET /v1/models and GET /v1/voices, plus /healthz, /readyz and /metrics.\n" +
			"Settings come from --config, KOKORO_RUN_* variables and flags; run `kokoro-run config` to see them.\n" +
			"Set KOKORO_RUN_API_KEY to require Authorization: Bearer <key> on /v1/.",
		Args: cobra.NoArgs,
	}
	frontendFlags(cmd)
	synthesisFlags(cmd)
	languageFlag(cmd)
	textPrepFlags(cmd)
	serverFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		l, err := loadSettings(cmd)
		if err != nil {
			return err
		}
		cfg := l.Config
		if err = cfg.Validate(true); err != nil {
			return err
		}
		voices, err := kokoro.ListVoices(cfg.VoicesFile)
		if err != nil {
			return fmt.Errorf("%w; set assets (KOKORO_RUN_ASSETS) or voices_file, or run kokoro-run pull", err)
		}
		if err = cfg.ValidateServer(voices); err != nil {
			return err
		}
		setLogger(cmd, cfg.Server.LogFormat)
		return serve(cmd.Context(), &cfg, voices)
	}
	return cmd
}

func setLogger(cmd *cobra.Command, format string) {
	var h slog.Handler = slog.NewJSONHandler(cmd.ErrOrStderr(), nil)
	if format == "text" {
		h = slog.NewTextHandler(cmd.ErrOrStderr(), nil)
	}
	slog.SetDefault(slog.New(h))
}

// serve loads the replicas, before listening with preload or while
// listening without it, and serves until ctx ends. Then it drains requests
// for up to the request timeout and closes the replicas.
func serve(ctx context.Context, cfg *settings.Config, voices []string) (err error) {
	pool := server.NewPool(cfg.Server.Replicas, cfg.Server.MaxQueue)
	defer func() { err = errors.Join(err, pool.Close()) }()
	c := fromSettings(*cfg)
	load := func(replica int) error {
		p, init, err := engine.Load(c.options(false), cfg.Voice, cfg.Speed)
		if err != nil {
			pool.Fail(err)
			return fmt.Errorf("loading replica %d: %w", replica, err)
		}
		// One short request first, so the first client doesn't pay for the
		// runtime's first-run setup.
		warm := time.Now()
		for _, err = range p.Stream(ctx, engine.Request{Text: "Ready.", Voice: cfg.Voice, Speed: cfg.Speed, Trim: cfg.Trim}) {
			if err != nil {
				err = errors.Join(fmt.Errorf("warming replica %d: %w", replica, err), p.Close())
				pool.Fail(err)
				return err
			}
		}
		pool.Add(p)
		slog.Info("replica loaded", "replica", replica, "seconds", init.TotalSeconds, "warm_up_seconds", time.Since(warm).Seconds(), "provider", cfg.Provider, "runtime_version", init.RuntimeVersion)
		return nil
	}
	if cfg.Server.Preload {
		for i := range cfg.Server.Replicas {
			if err := load(i); err != nil {
				return err
			}
		}
	} else {
		go func() {
			for i := range cfg.Server.Replicas {
				if err := load(i); err != nil {
					slog.Error("replica failed to load", "err", err)
				}
			}
		}()
	}

	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           server.New(cfg, pool, voices),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	slog.Info("listening", "addr", listener.Addr().String(), "replicas", cfg.Server.Replicas, "provider", cfg.Provider, "fallback", cfg.Fallback, "auth", cfg.Server.APIKey != "")
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err = <-served:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down", "drain_timeout", cfg.Server.Limits.RequestTimeout.String())
	shutdown, cancel := context.WithTimeout(context.Background(), cfg.Server.Limits.RequestTimeout)
	defer cancel()
	if err = srv.Shutdown(shutdown); err != nil {
		// Requests still running hold replicas; closing them now would crash those
		// requests' native calls, so exit without closing.
		fmt.Fprintln(os.Stderr, "requests still running after the drain timeout; exiting")
		os.Exit(1)
	}
	return nil
}
