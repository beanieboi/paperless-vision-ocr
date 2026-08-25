package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ben/paperless-macos-ocr/internal/jobs"
	"github.com/ben/paperless-macos-ocr/internal/ocr"
)

type fakeRunner struct {
	result ocr.Result
	err    error
	wait   chan struct{}
}

func (f *fakeRunner) Ready(context.Context) error { return nil }
func (f *fakeRunner) Process(ctx context.Context, request ocr.Request) (ocr.Result, error) {
	if f.wait != nil {
		select {
		case <-f.wait:
		case <-ctx.Done():
			return ocr.Result{}, ctx.Err()
		}
	}
	if f.err == nil {
		_ = os.WriteFile(request.OutputPath, []byte("%PDF-fake"), 0600)
	}
	return f.result, f.err
}

func waitForStatus(t *testing.T, repo *jobs.Repository, id string, status jobs.Status) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := repo.Get(id)
		if err == nil && job.Status == status {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, status)
	return jobs.Job{}
}

func TestManagerSuccess(t *testing.T) {
	repo := jobs.NewRepository(time.Hour)
	work := t.TempDir()
	now := time.Now().UTC()
	jobDir := filepath.Join(work, "job")
	_ = os.Mkdir(jobDir, 0700)
	job := jobs.Job{ID: "job", Status: jobs.NotStarted, CreatedAt: now, UpdatedAt: now, InputPath: filepath.Join(jobDir, "input.pdf"), OutputPDFPath: filepath.Join(jobDir, "output.pdf")}
	_ = repo.Create(job)
	manager := New(repo, &fakeRunner{result: ocr.Result{Text: "hello", OutputBytes: 9}}, work, 1, 1, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Shutdown(context.Background())
	if !manager.Submit("job") {
		t.Fatal("submit rejected")
	}
	got := waitForStatus(t, repo, "job", jobs.Succeeded)
	if got.OCRText != "hello" || got.OutputBytes != 9 {
		t.Fatalf("job = %#v", got)
	}
}

func TestManagerFailure(t *testing.T) {
	repo := jobs.NewRepository(time.Hour)
	work := t.TempDir()
	now := time.Now().UTC()
	jobDir := filepath.Join(work, "job")
	_ = os.Mkdir(jobDir, 0700)
	_ = repo.Create(jobs.Job{ID: "job", Status: jobs.NotStarted, CreatedAt: now, UpdatedAt: now, InputPath: filepath.Join(jobDir, "input.pdf"), OutputPDFPath: filepath.Join(jobDir, "output.pdf")})
	manager := New(repo, &fakeRunner{err: &ocr.ProcessError{Code: "SearchablePDFFailed", ExitCode: 7, Err: errors.New("boom")}}, work, 1, 1, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Shutdown(context.Background())
	manager.Submit("job")
	got := waitForStatus(t, repo, "job", jobs.Failed)
	if got.ErrorCode != "SearchablePDFFailed" || got.ExitCode != 7 {
		t.Fatalf("job = %#v", got)
	}
}

func TestManagerTimeout(t *testing.T) {
	repo := jobs.NewRepository(time.Hour)
	work := t.TempDir()
	now := time.Now().UTC()
	jobDir := filepath.Join(work, "job")
	_ = os.Mkdir(jobDir, 0700)
	_ = repo.Create(jobs.Job{ID: "job", Status: jobs.NotStarted, CreatedAt: now, UpdatedAt: now, InputPath: filepath.Join(jobDir, "input.pdf"), OutputPDFPath: filepath.Join(jobDir, "output.pdf")})
	manager := New(repo, &fakeRunner{err: &ocr.ProcessError{Code: "OCROperationTimedOut", ExitCode: -1, Timeout: true, Err: context.DeadlineExceeded}}, work, 1, 1, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Shutdown(context.Background())
	manager.Submit("job")
	got := waitForStatus(t, repo, "job", jobs.Failed)
	if got.ErrorCode != "OCROperationTimedOut" || !strings.Contains(got.ErrorMessage, "timeout") {
		t.Fatalf("job = %#v", got)
	}
}

func TestManagerBackpressure(t *testing.T) {
	wait := make(chan struct{})
	repo := jobs.NewRepository(time.Hour)
	work := t.TempDir()
	now := time.Now().UTC()
	for _, id := range []string{"one", "two", "three"} {
		dir := filepath.Join(work, id)
		_ = os.Mkdir(dir, 0700)
		_ = repo.Create(jobs.Job{ID: id, Status: jobs.NotStarted, CreatedAt: now, UpdatedAt: now, InputPath: filepath.Join(dir, "input.pdf"), OutputPDFPath: filepath.Join(dir, "output.pdf")})
	}
	manager := New(repo, &fakeRunner{wait: wait}, work, 1, 1, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !manager.Submit("one") {
		t.Fatal("first rejected")
	}
	_ = waitForStatus(t, repo, "one", jobs.Running)
	if !manager.Submit("two") {
		t.Fatal("second rejected")
	}
	if manager.Submit("three") {
		t.Fatal("full queue accepted third")
	}
	close(wait)
	_ = manager.Shutdown(context.Background())
}

func TestCleanupRemovesExpiredArtifacts(t *testing.T) {
	repo := jobs.NewRepository(time.Hour)
	work := t.TempDir()
	now := time.Now().UTC()
	id := "12345678-1234-4234-8234-123456789abc"
	jobDir := filepath.Join(work, id)
	if err := os.Mkdir(jobDir, 0700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(jobDir, "input.pdf")
	if err := os.WriteFile(input, []byte("pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = repo.Create(jobs.Job{ID: id, Status: jobs.Succeeded, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour), InputPath: input, OutputPDFPath: filepath.Join(jobDir, "output.pdf")})
	manager := New(repo, &fakeRunner{}, work, 1, 1, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Cleanup(now)
	defer manager.Shutdown(context.Background())
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job directory still exists: %v", err)
	}
	if _, err := repo.Get(id); !errors.Is(err, jobs.ErrExpired) {
		t.Fatalf("job error = %v", err)
	}
}
