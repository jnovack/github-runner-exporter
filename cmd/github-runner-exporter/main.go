package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/jnovack/flag"
	"github.com/jnovack/github-runner-exporter/internal/collector"
	"github.com/jnovack/github-runner-exporter/internal/runner"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Populated at build time via -ldflags; fall back to runtime/debug build info.
var (
	version      = "dev"
	buildRFC3339 = "1970-01-01T00:00:00Z"
	revision     = "local"
)

const (
	defaultVersion      = "dev"
	defaultBuildRFC3339 = "1970-01-01T00:00:00Z"
	defaultRevision     = "local"
)

// populateBuildMetadataFromBuildInfo fills in any values still at their
// defaults using Go's embedded VCS build info (set by `go build` or `go install`).
func populateBuildMetadataFromBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	if version == defaultVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if revision == defaultRevision && setting.Value != "" {
				revision = setting.Value
			}
		case "vcs.time":
			if buildRFC3339 == defaultBuildRFC3339 && setting.Value != "" {
				buildRFC3339 = setting.Value
			}
		}
	}
}

func main() {
	populateBuildMetadataFromBuildInfo()

	fs := flag.NewFlagSetWithEnvPrefix(os.Args[0], "", flag.ExitOnError)

	runnerDir := fs.String("runner-dir", runner.DefaultRunnerDir(),
		"Path to the GitHub Actions runner installation directory")
	listenAddress := fs.String("listen-address", ":9102",
		"Address on which to expose Prometheus metrics")
	logLevel := fs.String("log-level", "info",
		"Log verbosity: debug, info, warn, error")
	showVersion := fs.Bool("version", false,
		"Print version information and exit")

	_ = fs.Parse(os.Args[1:])

	setupLogging(*logLevel)

	if *showVersion {
		slog.Info("github-runner-exporter",
			"version", version,
			"build_rfc3339", buildRFC3339,
			"revision", revision,
		)
		os.Exit(0)
	}

	slog.Info("github-runner-exporter starting",
		"version", version,
		"build_rfc3339", buildRFC3339,
		"revision", revision,
		"runner_dir", *runnerDir,
		"listen", *listenAddress,
	)

	cfg, err := runner.LoadConfig(*runnerDir)
	if err != nil {
		slog.Error("failed to load runner config", "err", err)
		os.Exit(1)
	}

	slog.Info("runner config loaded",
		"runner_name", cfg.AgentName,
		"group", cfg.PoolName,
	)

	reg := prometheus.NewRegistry()
	tracker := runner.NewTracker(cfg.AgentName, reg)
	collector.New(tracker, cfg, version, revision, reg)

	diagDir := cfg.DiagDir(*runnerDir)
	watcher := runner.NewWatcher(diagDir, tracker)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("starting log watcher", "diag_dir", diagDir)
		if err := watcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("log watcher exited unexpectedly", "err", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:         *listenAddress,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("metrics server listening", "addr", *listenAddress)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown error", "err", err)
	}
}

func setupLogging(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}
