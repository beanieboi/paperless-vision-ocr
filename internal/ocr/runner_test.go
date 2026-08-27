package ocr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJSONL(t *testing.T) {
	text, err := parseJSONL(strings.NewReader("{\"text\":\"first\"}\n{\"text\":\"second\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "first\n\nsecond" {
		t.Fatalf("text = %q", text)
	}
}

func TestParseJSONLError(t *testing.T) {
	if _, err := parseJSONL(strings.NewReader("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseTranscriptJSONL(t *testing.T) {
	pages, err := parseTranscriptJSONL(strings.NewReader(
		"{\"page\":1,\"pageCount\":2,\"text\":\"first\",\"skipped\":false}\n" +
			"{\"page\":2,\"pageCount\":2,\"text\":\"\",\"skipped\":true}\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !pages.hasSkippedPages() || pages.text() != "first" {
		t.Fatalf("pages = %#v, text = %q", pages, pages.text())
	}
}

func TestParseTranscriptRejectsMissingAndMismatchedPages(t *testing.T) {
	for name, input := range map[string]string{
		"empty":       "",
		"starts at 2": `{"page":2,"pageCount":2,"text":"second","skipped":false}`,
		"wrong count": `{"page":1,"pageCount":2,"text":"first","skipped":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTranscriptJSONL(strings.NewReader(input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestTranscriptFallbackReplacesOnlySkippedPages(t *testing.T) {
	pages := transcriptPages{
		{Page: 1, PageCount: 2, Text: "partitioned scan"},
		{Page: 2, PageCount: 2, Skipped: true},
	}
	fallback := []string{"standard scan", "digital page"}
	for index := range pages {
		if pages[index].Skipped {
			pages[index].Text = fallback[index]
		}
	}
	if got := pages.text(); got != "partitioned scan\n\ndigital page" {
		t.Fatalf("text = %q", got)
	}
}

func TestAppendLanguages(t *testing.T) {
	got := appendLanguages([]string{"input"}, []string{"de-DE", "en-US"})
	if strings.Join(got, " ") != "input -l de-DE -l en-US" {
		t.Fatalf("args = %#v", got)
	}
}

func TestMacRunnerUsesSearchablePDFTranscriptWithoutSecondPass(t *testing.T) {
	runner, logPath, inputPath, outputPath := fakeMacRunner(t,
		`{"page":1,"pageCount":1,"text":"partitioned text","skipped":false}`,
		`{"text":"must not be used"}`,
	)

	result, err := runner.Process(context.Background(), Request{InputPath: inputPath, OutputPath: outputPath})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "partitioned text" {
		t.Fatalf("text = %q", result.Text)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "searchable-pdf ") {
		t.Fatalf("invocations = %q", lines)
	}
}

func TestMacRunnerFallsBackOnlyForSkippedPages(t *testing.T) {
	runner, logPath, inputPath, outputPath := fakeMacRunner(t,
		"{\"page\":1,\"pageCount\":2,\"text\":\"partitioned scan\",\"skipped\":false}\n"+
			`{"page":2,"pageCount":2,"text":"","skipped":true}`,
		"{\"text\":\"standard scan\"}\n{\"text\":\"digital page\"}",
	)

	result, err := runner.Process(context.Background(), Request{InputPath: inputPath, OutputPath: outputPath})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "partitioned scan\n\ndigital page" {
		t.Fatalf("text = %q", result.Text)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "searchable-pdf ") || strings.Contains(lines[1], "searchable-pdf") {
		t.Fatalf("invocations = %q", lines)
	}
}

func fakeMacRunner(t *testing.T, transcript, fallback string) (*MacRunner, string, string, string) {
	t.Helper()
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "fake-mac-ocr")
	logPath := filepath.Join(directory, "invocations.log")
	inputPath := filepath.Join(directory, "input.pdf")
	outputPath := filepath.Join(directory, "output.pdf")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_MAC_OCR_LOG"
if [ "$1" = "searchable-pdf" ]; then
  output=""
  transcript=""
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -o) output="$2"; shift 2 ;;
      --transcript-output) transcript="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  printf '%%PDF-1.4\n' > "$output"
  printf '%s\n' "$FAKE_MAC_OCR_TRANSCRIPT" > "$transcript"
else
  printf '%s\n' "$FAKE_MAC_OCR_FALLBACK"
fi
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MAC_OCR_LOG", logPath)
	t.Setenv("FAKE_MAC_OCR_TRANSCRIPT", transcript)
	t.Setenv("FAKE_MAC_OCR_FALLBACK", fallback)
	return &MacRunner{Path: scriptPath, Languages: []string{"de-DE", "en-US"}, Strategy: "auto", Timeout: 5 * time.Second}, logPath, inputPath, outputPath
}
