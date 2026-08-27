package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host              string
	Port              int
	Languages         []string
	Strategy          string
	MaxConcurrentJobs int
	MaxQueuedJobs     int
	OCRTimeout        time.Duration
	JobTTL            time.Duration
	WorkDir           string
	APIKey            string
	MaxUploadBytes    int64
	MacOCRPath        string
	LogLevel          slog.Level
}

func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	get := func(key, fallback string) string {
		if value, ok := lookup(key); ok {
			return strings.TrimSpace(value)
		}
		return fallback
	}
	parseInt := func(key string, fallback, min, max int) (int, error) {
		value, err := strconv.Atoi(get(key, strconv.Itoa(fallback)))
		if err != nil || value < min || value > max {
			return 0, fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
		}
		return value, nil
	}

	port, err := parseInt("PORT", 8080, 1, 65535)
	if err != nil {
		return Config{}, err
	}
	concurrency, err := parseInt("OCR_MAX_CONCURRENT_JOBS", 1, 1, 128)
	if err != nil {
		return Config{}, err
	}
	queued, err := parseInt("OCR_MAX_QUEUED_JOBS", 20, 1, 10000)
	if err != nil {
		return Config{}, err
	}
	timeoutMinutes, err := parseInt("OCR_TIMEOUT_MINUTES", 30, 1, 24*60)
	if err != nil {
		return Config{}, err
	}
	ttlHours, err := parseInt("OCR_JOB_TTL_HOURS", 24, 1, 24*365)
	if err != nil {
		return Config{}, err
	}
	maxUploadMB, err := parseInt("OCR_MAX_UPLOAD_MB", 100, 1, 10240)
	if err != nil {
		return Config{}, err
	}

	languageValue := get("OCR_LANGUAGES", "de-DE,en-US")
	var languages []string
	seen := make(map[string]bool)
	for language := range strings.SplitSeq(languageValue, ",") {
		language = strings.TrimSpace(language)
		if language == "" {
			return Config{}, fmt.Errorf("OCR_LANGUAGES contains an empty language")
		}
		key := strings.ToLower(language)
		if !seen[key] {
			languages = append(languages, language)
			seen[key] = true
		}
	}
	if len(languages) == 0 {
		return Config{}, fmt.Errorf("OCR_LANGUAGES must not be empty")
	}

	strategy := strings.ToLower(get("OCR_STRATEGY", "standard"))
	if strategy != "auto" && strategy != "standard" && strategy != "partitioned" {
		return Config{}, fmt.Errorf("OCR_STRATEGY must be auto, standard, or partitioned")
	}

	levelName := strings.ToUpper(get("LOG_LEVEL", "INFO"))
	levels := map[string]slog.Level{"DEBUG": slog.LevelDebug, "INFO": slog.LevelInfo, "WARN": slog.LevelWarn, "WARNING": slog.LevelWarn, "ERROR": slog.LevelError}
	level, ok := levels[levelName]
	if !ok {
		return Config{}, fmt.Errorf("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}

	host := get("HOST", "0.0.0.0")
	if host == "" || strings.ContainsAny(host, "/\x00") {
		return Config{}, fmt.Errorf("HOST is invalid")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil && strings.Contains(host, ":") {
		return Config{}, fmt.Errorf("HOST is invalid")
	}

	workDir := get("OCR_WORK_DIR", filepath.Join(os.TempDir(), "paperless-vision-ocr"))
	if !filepath.IsAbs(workDir) {
		return Config{}, fmt.Errorf("OCR_WORK_DIR must be absolute")
	}
	macOCRPath := get("MAC_OCR_PATH", "mac-ocr")
	if macOCRPath == "" {
		return Config{}, fmt.Errorf("MAC_OCR_PATH must not be empty")
	}

	return Config{
		Host: host, Port: port, Languages: languages, Strategy: strategy,
		MaxConcurrentJobs: concurrency, MaxQueuedJobs: queued,
		OCRTimeout: time.Duration(timeoutMinutes) * time.Minute,
		JobTTL:     time.Duration(ttlHours) * time.Hour,
		WorkDir:    filepath.Clean(workDir), APIKey: get("OCR_API_KEY", ""),
		MaxUploadBytes: int64(maxUploadMB) * 1024 * 1024,
		MacOCRPath:     macOCRPath, LogLevel: level,
	}, nil
}

func (c Config) Address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
