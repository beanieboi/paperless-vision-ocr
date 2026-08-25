package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Request struct {
	InputPath  string
	OutputPath string
}

type Result struct {
	Text        string
	Duration    time.Duration
	OutputBytes int64
	ExitCode    int
}

type ProcessError struct {
	Code       string
	Diagnostic string
	ExitCode   int
	Timeout    bool
	Err        error
}

func (e *ProcessError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}
func (e *ProcessError) Unwrap() error { return e.Err }

type Runner interface {
	Process(context.Context, Request) (Result, error)
	Ready(context.Context) error
}

type MacRunner struct {
	Path      string
	Languages []string
	Strategy  string
	Timeout   time.Duration
}

func (r *MacRunner) executable() (string, error) {
	if strings.ContainsRune(r.Path, filepath.Separator) {
		info, err := os.Stat(r.Path)
		if err != nil {
			return "", err
		}
		if info.IsDir() || info.Mode().Perm()&0111 == 0 {
			return "", fmt.Errorf("%s is not executable", r.Path)
		}
		return r.Path, nil
	}
	return exec.LookPath(r.Path)
}

func (r *MacRunner) Ready(ctx context.Context) error {
	path, err := r.executable()
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, "--version")
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("execute %s --version: %w: %s", path, err, stderr.String())
	}
	return nil
}

func (r *MacRunner) Version(ctx context.Context) string {
	path, err := r.executable()
	if err != nil {
		return "unavailable"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func (r *MacRunner) ValidateLanguages(ctx context.Context) error {
	path, err := r.executable()
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, "languages")
	var stdout, stderr limitedBuffer
	stdout.limit = 1024 * 1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("list mac-ocr languages: %w: %s", err, stderr.String())
	}
	supported := make(map[string]bool)
	for language := range strings.FieldsSeq(stdout.String()) {
		supported[strings.ToLower(language)] = true
	}
	for _, language := range r.Languages {
		if !supported[strings.ToLower(language)] {
			return fmt.Errorf("mac-ocr does not support configured language %q", language)
		}
	}
	return nil
}

func (r *MacRunner) Process(ctx context.Context, request Request) (Result, error) {
	started := time.Now()
	path, err := r.executable()
	if err != nil {
		return Result{}, &ProcessError{Code: "ExecutableNotFound", ExitCode: -1, Err: err}
	}
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	textArgs := []string{request.InputPath, "--format", "jsonl"}
	textArgs = appendLanguages(textArgs, r.Languages)
	var textStderr limitedBuffer
	cmd := exec.CommandContext(runCtx, path, textArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Duration: time.Since(started), ExitCode: -1}, &ProcessError{Code: "OCRTextFailed", ExitCode: -1, Err: err}
	}
	cmd.Stderr = &textStderr
	if err := cmd.Start(); err != nil {
		return Result{Duration: time.Since(started), ExitCode: exitCode(err)}, processError(runCtx, "OCRTextFailed", err, textStderr.String())
	}
	text, parseErr := parseJSONL(stdout)
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return Result{Duration: time.Since(started), ExitCode: exitCode(waitErr)}, processError(runCtx, "OCRTextFailed", waitErr, textStderr.String())
	}
	if parseErr != nil {
		return Result{Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "InvalidOCROutput", ExitCode: 0, Err: fmt.Errorf("parse mac-ocr JSONL: %w", parseErr)}
	}

	pdfArgs := []string{"searchable-pdf", request.InputPath, "-o", request.OutputPath, "--ocr-strategy", r.Strategy}
	pdfArgs = appendLanguages(pdfArgs, r.Languages)
	var pdfStderr limitedBuffer
	cmd = exec.CommandContext(runCtx, path, pdfArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = &pdfStderr
	if err := cmd.Run(); err != nil {
		return Result{Text: text, Duration: time.Since(started), ExitCode: exitCode(err)}, processError(runCtx, "SearchablePDFFailed", err, pdfStderr.String())
	}
	info, err := os.Stat(request.OutputPath)
	if err != nil {
		return Result{Text: text, Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "OutputMissing", ExitCode: 0, Err: fmt.Errorf("stat searchable PDF: %w", err)}
	}
	if info.Size() == 0 {
		return Result{Text: text, Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "OutputMissing", ExitCode: 0, Err: errors.New("mac-ocr produced an empty PDF")}
	}
	return Result{Text: text, Duration: time.Since(started), OutputBytes: info.Size(), ExitCode: 0}, nil
}

func appendLanguages(args []string, languages []string) []string {
	for _, language := range languages {
		args = append(args, "-l", language)
	}
	return args
}

func parseJSONL(reader io.Reader) (string, error) {
	decoder := json.NewDecoder(reader)
	var pages []string
	for {
		var page struct {
			Text string `json:"text"`
		}
		if err := decoder.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		if page.Text != "" {
			pages = append(pages, page.Text)
		}
	}
	return strings.Join(pages, "\n\n"), nil
}

func processError(ctx context.Context, code string, err error, diagnostic string) *ProcessError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ProcessError{Code: "OCROperationTimedOut", Diagnostic: diagnostic, ExitCode: exitCode(err), Timeout: true, Err: ctx.Err()}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return &ProcessError{Code: "OCROperationCanceled", Diagnostic: diagnostic, ExitCode: exitCode(err), Err: ctx.Err()}
	}
	return &ProcessError{Code: code, Diagnostic: diagnostic, ExitCode: exitCode(err), Err: err}
}

func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	if err == nil {
		return 0
	}
	return -1
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	limit := b.limit
	if limit == 0 {
		limit = 64 * 1024
	}
	original := len(value)
	if b.Len() < limit {
		remaining := limit - b.Len()
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}
