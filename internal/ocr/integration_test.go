package ocr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMacOCRIntegration(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple Vision requires macOS")
	}
	path, err := exec.LookPath("mac-ocr")
	if err != nil {
		t.Skip("mac-ocr is not installed")
	}
	fixture := filepath.Join("..", "..", "testdata", "rechnung-image-only.pdf")
	if _, err := os.Stat(fixture); err != nil {
		t.Skip("OCR fixture is not present")
	}
	output := filepath.Join(t.TempDir(), "output.pdf")
	runner := &MacRunner{Path: path, Languages: []string{"de-DE", "en-US"}, Strategy: "auto", Timeout: 5 * time.Minute}
	result, err := runner.Process(context.Background(), Request{InputPath: fixture, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		t.Fatal("output is not a PDF")
	}
	if !strings.Contains(result.Text, "Rechnung") || !strings.Contains(result.Text, "12345") {
		t.Fatalf("OCR text did not contain fixture terms: %q", result.Text)
	}
	if pdftotext, err := exec.LookPath("pdftotext"); err == nil {
		textPath := filepath.Join(t.TempDir(), "text.txt")
		if err := exec.Command(pdftotext, output, textPath).Run(); err != nil {
			t.Fatal(err)
		}
		text, err := os.ReadFile(textPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), "Rechnung") || !strings.Contains(string(text), "12345") {
			t.Fatalf("PDF text layer missing terms: %q", text)
		}
	}
}
