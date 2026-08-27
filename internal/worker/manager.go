package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beanieboi/paperless-vision-ocr/internal/jobs"
	"github.com/beanieboi/paperless-vision-ocr/internal/ocr"
)

var jobDirectoryPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Manager struct {
	repo      *jobs.Repository
	runner    ocr.Runner
	queue     chan string
	workDir   string
	ttl       time.Duration
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	submitMu  sync.RWMutex
	accepting atomic.Bool
}

func New(repo *jobs.Repository, runner ocr.Runner, workDir string, concurrency, queued int, ttl time.Duration, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{repo: repo, runner: runner, queue: make(chan string, queued), workDir: workDir, ttl: ttl, logger: logger, ctx: ctx, cancel: cancel}
	manager.accepting.Store(true)
	for i := range concurrency {
		manager.wg.Add(1)
		go manager.worker(i + 1)
	}
	manager.wg.Add(1)
	go manager.cleanupLoop()
	return manager
}

func (m *Manager) Submit(id string) bool {
	m.submitMu.RLock()
	defer m.submitMu.RUnlock()
	if !m.accepting.Load() {
		return false
	}
	select {
	case m.queue <- id:
		return true
	default:
		return false
	}
}

func (m *Manager) StopAccepting() {
	m.submitMu.Lock()
	m.accepting.Store(false)
	m.submitMu.Unlock()
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.StopAccepting()
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) worker(number int) {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case id := <-m.queue:
			m.process(id, number)
		}
	}
}

func (m *Manager) process(id string, number int) {
	job, err := m.repo.Get(id)
	if err != nil {
		return
	}
	started := time.Now().UTC()
	_ = m.repo.Update(id, func(current *jobs.Job) {
		current.Status = jobs.Running
		current.StartedAt = started
		current.UpdatedAt = started
		current.QueueDuration = max(started.Sub(current.CreatedAt)-current.UploadDuration, 0)
	})
	m.logger.Info("OCR job started", "job_id", id, "worker", number, "status", jobs.Running, "input_bytes", job.InputBytes)

	result, runErr := m.runner.Process(m.ctx, ocr.Request{InputPath: job.InputPath, OutputPath: job.OutputPDFPath})
	finished := time.Now().UTC()
	if runErr != nil {
		code, publicMessage, diagnostic, exitCode := "OCROperationFailed", "Local OCR processing failed.", runErr.Error(), result.ExitCode
		if processErr, ok := errors.AsType[*ocr.ProcessError](runErr); ok {
			code, diagnostic, exitCode = processErr.Code, processErr.Diagnostic, processErr.ExitCode
			if processErr.Timeout {
				publicMessage = "Local OCR processing exceeded its configured timeout."
			}
		}
		_ = m.repo.Update(id, func(current *jobs.Job) {
			current.Status = jobs.Failed
			current.UpdatedAt = finished
			current.FinishedAt = finished
			current.Duration = finished.Sub(current.CreatedAt)
			current.OCRDuration = result.Duration
			current.ErrorCode = code
			current.ErrorMessage = publicMessage
			current.Diagnostic = diagnostic
			current.ExitCode = exitCode
		})
		m.logger.Error("OCR job failed", "job_id", id, "status", jobs.Failed, "duration_ms", finished.Sub(job.CreatedAt).Milliseconds(), "ocr_duration_ms", result.Duration.Milliseconds(), "mac_ocr_exit_code", exitCode, "error", runErr)
		return
	}
	_ = m.repo.Update(id, func(current *jobs.Job) {
		current.Status = jobs.Succeeded
		current.UpdatedAt = finished
		current.FinishedAt = finished
		current.Duration = finished.Sub(current.CreatedAt)
		current.OCRDuration = result.Duration
		current.OCRText = result.Text
		current.OutputBytes = result.OutputBytes
		current.ExitCode = result.ExitCode
	})
	m.logger.Info("OCR job succeeded", "job_id", id, "status", jobs.Succeeded, "input_bytes", job.InputBytes, "output_bytes", result.OutputBytes, "queue_duration_ms", started.Sub(job.CreatedAt).Milliseconds(), "ocr_duration_ms", result.Duration.Milliseconds(), "duration_ms", finished.Sub(job.CreatedAt).Milliseconds(), "mac_ocr_exit_code", result.ExitCode)
}

func (m *Manager) cleanupLoop() {
	defer m.wg.Done()
	interval := min(m.ttl/2, time.Hour)
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.Cleanup(now.UTC())
		}
	}
}

func (m *Manager) Cleanup(now time.Time) {
	for _, job := range m.repo.Expire(now, m.ttl) {
		directory := filepath.Dir(job.InputPath)
		if err := os.RemoveAll(directory); err != nil {
			m.logger.Warn("failed to clean expired job", "job_id", job.ID, "error", err)
		} else {
			m.logger.Info("expired job cleaned", "job_id", job.ID)
		}
	}
	entries, err := os.ReadDir(m.workDir)
	if err != nil {
		m.logger.Warn("failed to scan work directory", "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !jobDirectoryPattern.MatchString(entry.Name()) {
			continue
		}
		directory := filepath.Join(m.workDir, entry.Name())
		if m.repo.ActiveDirectory(directory) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < m.ttl {
			continue
		}
		if err := os.RemoveAll(directory); err != nil {
			m.logger.Warn("failed to clean orphaned directory", "directory", directory, "error", err)
		}
	}
}
