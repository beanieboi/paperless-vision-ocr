package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ben/paperless-macos-ocr/internal/api"
	"github.com/ben/paperless-macos-ocr/internal/jobs"
	"github.com/ben/paperless-macos-ocr/internal/ocr"
	"github.com/ben/paperless-macos-ocr/internal/worker"
)

type fakeRunner struct{}

func (*fakeRunner) Ready(context.Context) error { return nil }
func (*fakeRunner) Process(_ context.Context, request ocr.Request) (ocr.Result, error) {
	data, err := os.ReadFile(request.InputPath)
	if err != nil {
		return ocr.Result{}, err
	}
	if err := os.WriteFile(request.OutputPath, data, 0600); err != nil {
		return ocr.Result{}, err
	}
	return ocr.Result{Text: "SDK compatibility text", OutputBytes: int64(len(data)), ExitCode: 0}, nil
}

func main() {
	work, err := os.MkdirTemp("", "paperless-macos-ocr-compat-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(work)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := jobs.NewRepository(time.Hour)
	runner := &fakeRunner{}
	manager := worker.New(repo, runner, work, 1, 2, time.Hour, logger)
	defer manager.Shutdown(context.Background())
	server := api.New(repo, manager, runner, work, "dummy", 100*1024*1024, logger)
	if err := http.ListenAndServe("127.0.0.1:18080", server.Handler()); err != nil {
		panic(err)
	}
}
