package azure

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestDecodeAnalyzeRequest(t *testing.T) {
	pdf := []byte("%PDF-example")
	body := ` { "base64Source" : "` + base64.StdEncoding.EncodeToString(pdf) + `" } `
	var output bytes.Buffer
	n, err := DecodeAnalyzeRequest(strings.NewReader(body), &output, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(pdf)) || !bytes.Equal(output.Bytes(), pdf) {
		t.Fatalf("output %q, n=%d", output.Bytes(), n)
	}
}

func TestDecodeAnalyzeRequestRejectsMalformed(t *testing.T) {
	for _, body := range []string{`{}`, `{"urlSource":"https://example"}`, `{"base64Source":"%%%"}`, `{"base64Source":"Zg==","other":1}`} {
		if _, err := DecodeAnalyzeRequest(strings.NewReader(body), &bytes.Buffer{}, 100); !errors.Is(err, ErrMalformedRequest) {
			t.Errorf("body %s: error %v", body, err)
		}
	}
}

func TestDecodeAnalyzeRequestLimit(t *testing.T) {
	body := `{"base64Source":"` + base64.StdEncoding.EncodeToString([]byte("too large")) + `"}`
	if _, err := DecodeAnalyzeRequest(strings.NewReader(body), &bytes.Buffer{}, 3); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("error = %v", err)
	}
}
