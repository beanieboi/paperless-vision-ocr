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
	helpCmd := exec.CommandContext(probeCtx, path, "searchable-pdf", "--help")
	help, err := helpCmd.Output()
	if err != nil {
		return fmt.Errorf("execute %s searchable-pdf --help: %w", path, err)
	}
	if !bytes.Contains(help, []byte("--transcript-output")) {
		return fmt.Errorf("%s does not support --transcript-output; install the paperless-vision-ocr pinned mac-ocr build", path)
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

	transcriptPath := filepath.Join(filepath.Dir(request.OutputPath), "transcript.jsonl")
	_ = os.Remove(transcriptPath)
	pdfArgs := []string{
		"searchable-pdf", request.InputPath,
		"-o", request.OutputPath,
		"--ocr-strategy", r.Strategy,
		"--transcript-output", transcriptPath,
	}
	pdfArgs = appendLanguages(pdfArgs, r.Languages)
	var pdfStderr limitedBuffer
	cmd := exec.CommandContext(runCtx, path, pdfArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = &pdfStderr
	if err := cmd.Run(); err != nil {
		return Result{Duration: time.Since(started), ExitCode: exitCode(err)}, processError(runCtx, "SearchablePDFFailed", err, pdfStderr.String())
	}
	transcriptFile, err := os.Open(transcriptPath)
	if err != nil {
		return Result{Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "InvalidOCROutput", ExitCode: 0, Err: fmt.Errorf("open mac-ocr transcript: %w", err)}
	}
	transcript, parseErr := parseTranscriptJSONL(transcriptFile)
	closeErr := transcriptFile.Close()
	if parseErr != nil {
		return Result{Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "InvalidOCROutput", ExitCode: 0, Err: fmt.Errorf("parse mac-ocr transcript: %w", parseErr)}
	}
	if closeErr != nil {
		return Result{Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "InvalidOCROutput", ExitCode: 0, Err: fmt.Errorf("close mac-ocr transcript: %w", closeErr)}
	}

	if transcript.hasSkippedPages() {
		fallbackPages, fallbackErr := r.recognizeText(runCtx, path, request.InputPath)
		if fallbackErr != nil {
			return Result{Duration: time.Since(started), ExitCode: processExitCode(fallbackErr)}, fallbackErr
		}
		if len(fallbackPages) != len(transcript) {
			return Result{Duration: time.Since(started), ExitCode: 0}, &ProcessError{
				Code:     "InvalidOCROutput",
				ExitCode: 0,
				Err:      fmt.Errorf("mac-ocr page count mismatch: transcript has %d pages, fallback has %d", len(transcript), len(fallbackPages)),
			}
		}
		for index := range transcript {
			if transcript[index].Skipped {
				transcript[index].Text = fallbackPages[index]
			}
		}
	}
	text := transcript.text()

	info, err := os.Stat(request.OutputPath)
	if err != nil {
		return Result{Text: text, Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "OutputMissing", ExitCode: 0, Err: fmt.Errorf("stat searchable PDF: %w", err)}
	}
	if info.Size() == 0 {
		return Result{Text: text, Duration: time.Since(started), ExitCode: 0}, &ProcessError{Code: "OutputMissing", ExitCode: 0, Err: errors.New("mac-ocr produced an empty PDF")}
	}
	return Result{Text: text, Duration: time.Since(started), OutputBytes: info.Size(), ExitCode: 0}, nil
}

func (r *MacRunner) recognizeText(ctx context.Context, path, inputPath string) ([]string, error) {
	textArgs := []string{inputPath, "--format", "jsonl"}
	textArgs = appendLanguages(textArgs, r.Languages)
	var textStderr limitedBuffer
	cmd := exec.CommandContext(ctx, path, textArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ProcessError{Code: "OCRTextFailed", ExitCode: -1, Err: err}
	}
	cmd.Stderr = &textStderr
	if err := cmd.Start(); err != nil {
		return nil, processError(ctx, "OCRTextFailed", err, textStderr.String())
	}
	pages, parseErr := parseTextPagesJSONL(stdout)
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, processError(ctx, "OCRTextFailed", waitErr, textStderr.String())
	}
	if parseErr != nil {
		return nil, &ProcessError{Code: "InvalidOCROutput", ExitCode: 0, Err: fmt.Errorf("parse mac-ocr JSONL: %w", parseErr)}
	}
	return pages, nil
}

func appendLanguages(args []string, languages []string) []string {
	for _, language := range languages {
		args = append(args, "-l", language)
	}
	return args
}

func parseJSONL(reader io.Reader) (string, error) {
	pages, err := parseTextPagesJSONL(reader)
	if err != nil {
		return "", err
	}
	return strings.Join(nonEmptyPages(pages), "\n\n"), nil
}

func parseTextPagesJSONL(reader io.Reader) ([]string, error) {
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
			return nil, err
		}
		pages = append(pages, page.Text)
	}
	return pages, nil
}

type transcriptPage struct {
	Page      int    `json:"page"`
	PageCount int    `json:"pageCount"`
	Text      string `json:"text"`
	Skipped   bool   `json:"skipped"`
}

type transcriptPages []transcriptPage

func parseTranscriptJSONL(reader io.Reader) (transcriptPages, error) {
	decoder := json.NewDecoder(reader)
	var pages transcriptPages
	for {
		var page transcriptPage
		if err := decoder.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if page.Page != len(pages)+1 || page.PageCount < 1 {
			return nil, fmt.Errorf("unexpected page metadata: page %d of %d", page.Page, page.PageCount)
		}
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		return nil, errors.New("empty transcript")
	}
	for _, page := range pages {
		if page.PageCount != len(pages) {
			return nil, fmt.Errorf("page count %d does not match %d records", page.PageCount, len(pages))
		}
	}
	return pages, nil
}

func (p transcriptPages) hasSkippedPages() bool {
	for _, page := range p {
		if page.Skipped {
			return true
		}
	}
	return false
}

func (p transcriptPages) text() string {
	pages := make([]string, len(p))
	for index := range p {
		pages[index] = p[index].Text
	}
	return strings.Join(nonEmptyPages(pages), "\n\n")
}

func nonEmptyPages(pages []string) []string {
	result := make([]string, 0, len(pages))
	for _, page := range pages {
		if page != "" {
			result = append(result, page)
		}
	}
	return result
}

func processExitCode(err error) int {
	var processErr *ProcessError
	if errors.As(err, &processErr) {
		return processErr.ExitCode
	}
	return -1
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
