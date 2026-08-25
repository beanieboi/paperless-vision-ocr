package ocr

import (
	"strings"
	"testing"
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

func TestAppendLanguages(t *testing.T) {
	got := appendLanguages([]string{"input"}, []string{"de-DE", "en-US"})
	if strings.Join(got, " ") != "input -l de-DE -l en-US" {
		t.Fatalf("args = %#v", got)
	}
}
