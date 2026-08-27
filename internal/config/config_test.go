package config

import (
	"log/slog"
	"testing"
	"time"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestDefaults(t *testing.T) {
	cfg, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address() != "0.0.0.0:8080" {
		t.Fatalf("address = %q", cfg.Address())
	}
	if len(cfg.Languages) != 2 || cfg.Languages[0] != "de-DE" || cfg.Languages[1] != "en-US" {
		t.Fatalf("languages = %#v", cfg.Languages)
	}
	if cfg.Strategy != "auto" || cfg.MaxConcurrentJobs != 1 || cfg.MaxQueuedJobs != 20 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.OCRTimeout != 30*time.Minute || cfg.JobTTL != 24*time.Hour || cfg.MaxUploadBytes != 100*1024*1024 {
		t.Fatalf("unexpected limits: %#v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("log level = %v", cfg.LogLevel)
	}
}

func TestEnvironmentParsing(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"HOST": "127.0.0.1", "PORT": "9000", "OCR_LANGUAGES": " en-US, de-DE,en-us ",
		"OCR_STRATEGY": "partitioned", "OCR_MAX_CONCURRENT_JOBS": "3", "OCR_MAX_QUEUED_JOBS": "7",
		"OCR_TIMEOUT_MINUTES": "9", "OCR_JOB_TTL_HOURS": "4", "OCR_WORK_DIR": "/tmp/custom",
		"OCR_MAX_UPLOAD_MB": "12", "MAC_OCR_PATH": "/bin/mac-ocr", "LOG_LEVEL": "debug",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Languages) != 2 || cfg.MaxUploadBytes != 12*1024*1024 || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestInvalidValues(t *testing.T) {
	tests := []map[string]string{
		{"PORT": "0"}, {"OCR_LANGUAGES": "de-DE,"}, {"OCR_STRATEGY": "magic"},
		{"OCR_MAX_CONCURRENT_JOBS": "0"}, {"OCR_MAX_QUEUED_JOBS": "0"},
		{"OCR_TIMEOUT_MINUTES": "no"}, {"OCR_JOB_TTL_HOURS": "0"}, {"OCR_MAX_UPLOAD_MB": "0"},
		{"OCR_WORK_DIR": "relative"}, {"LOG_LEVEL": "TRACE"},
	}
	for _, values := range tests {
		if _, err := load(env(values)); err == nil {
			t.Errorf("expected error for %#v", values)
		}
	}
}
