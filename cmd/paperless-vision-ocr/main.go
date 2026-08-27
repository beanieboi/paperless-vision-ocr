package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/beanieboi/paperless-vision-ocr/internal/api"
	"github.com/beanieboi/paperless-vision-ocr/internal/config"
	"github.com/beanieboi/paperless-vision-ocr/internal/jobs"
	"github.com/beanieboi/paperless-vision-ocr/internal/ocr"
	"github.com/beanieboi/paperless-vision-ocr/internal/worker"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "paperless-vision-ocr:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	if err := os.MkdirAll(cfg.WorkDir, 0700); err != nil {
		return fmt.Errorf("create work directory %s: %w", cfg.WorkDir, err)
	}
	if err := checkWritable(cfg.WorkDir); err != nil {
		return fmt.Errorf("work directory %s is not writable: %w", cfg.WorkDir, err)
	}

	runner := &ocr.MacRunner{Path: cfg.MacOCRPath, Languages: cfg.Languages, Strategy: cfg.Strategy, Timeout: cfg.OCRTimeout}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	if err := runner.Ready(startupCtx); err != nil {
		return fmt.Errorf("mac-ocr executable not found or unusable at %q: %w\n\nInstall the pinned native binary with scripts/install-mac-ocr.sh (see README), or configure MAC_OCR_PATH", cfg.MacOCRPath, err)
	}
	if err := runner.ValidateLanguages(startupCtx); err != nil {
		return fmt.Errorf("mac-ocr language validation: %w", err)
	}

	repo := jobs.NewRepository(cfg.JobTTL)
	manager := worker.New(repo, runner, cfg.WorkDir, cfg.MaxConcurrentJobs, cfg.MaxQueuedJobs, cfg.JobTTL, logger)
	apiServer := api.New(repo, manager, runner, cfg.WorkDir, cfg.APIKey, cfg.MaxUploadBytes, logger)
	httpServer := &http.Server{
		Addr: cfg.Address(), Handler: apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute,
		WriteTimeout: 10 * time.Minute, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}

	logger.Info("service starting",
		"service", "paperless-vision-ocr", "version", version, "go_version", runtime.Version(),
		"mac_ocr_path", cfg.MacOCRPath, "mac_ocr_version", runner.Version(startupCtx),
		"address", cfg.Address(), "work_dir", cfg.WorkDir, "languages", cfg.Languages,
		"ocr_strategy", cfg.Strategy, "max_concurrent_jobs", cfg.MaxConcurrentJobs,
		"max_queued_jobs", cfg.MaxQueuedJobs, "ocr_timeout", cfg.OCRTimeout.String(),
		"upload_limit_bytes", cfg.MaxUploadBytes, "job_ttl", cfg.JobTTL.String(),
	)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serverError := make(chan error, 1)
	go func() { serverError <- httpServer.ListenAndServe() }()
	select {
	case <-signalCtx.Done():
		logger.Info("shutdown requested")
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			manager.StopAccepting()
			_ = manager.Shutdown(context.Background())
			return fmt.Errorf("HTTP server: %w", err)
		}
	}

	manager.StopAccepting()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP shutdown incomplete", "error", err)
	}
	if err := manager.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("worker shutdown: %w", err)
	}
	logger.Info("service stopped")
	return nil
}

func checkWritable(directory string) error {
	file, err := os.CreateTemp(directory, ".startup-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
