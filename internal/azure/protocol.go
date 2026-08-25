package azure

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

const (
	APIVersion = "2024-11-30"
	ModelID    = "prebuilt-read"
)

var (
	ErrMalformedRequest = errors.New("malformed analyze request")
	ErrUploadTooLarge   = errors.New("upload too large")
)

// DecodeAnalyzeRequest decodes the one-field JSON object emitted by
// AnalyzeDocumentRequest(bytes_source=...). The base64 value is streamed to
// dst so large PDFs are not materialized in memory.
func DecodeAnalyzeRequest(source io.Reader, dst io.Writer, maxDecoded int64) (int64, error) {
	reader := bufio.NewReader(source)
	value, err := nextNonSpace(reader)
	if err != nil || value != '{' {
		return 0, ErrMalformedRequest
	}
	key, err := readJSONString(reader, 128)
	if err != nil || key != "base64Source" {
		return 0, fmt.Errorf("%w: expected base64Source", ErrMalformedRequest)
	}
	value, err = nextNonSpace(reader)
	if err != nil || value != ':' {
		return 0, ErrMalformedRequest
	}
	value, err = nextNonSpace(reader)
	if err != nil || value != '"' {
		return 0, ErrMalformedRequest
	}

	encoded := &jsonStringReader{reader: reader}
	decoder := base64.NewDecoder(base64.StdEncoding, encoded)
	written, copyErr := io.Copy(dst, io.LimitReader(decoder, maxDecoded+1))
	if written > maxDecoded {
		return written, ErrUploadTooLarge
	}
	if copyErr != nil {
		return written, fmt.Errorf("%w: invalid base64Source: %v", ErrMalformedRequest, copyErr)
	}
	if encoded.err != nil {
		return written, fmt.Errorf("%w: %v", ErrMalformedRequest, encoded.err)
	}
	if !encoded.ended {
		return written, ErrMalformedRequest
	}
	value, err = nextNonSpace(reader)
	if err != nil || value != '}' {
		return written, fmt.Errorf("%w: unexpected field", ErrMalformedRequest)
	}
	for {
		value, err = reader.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return written, ErrMalformedRequest
		}
		if !unicode.IsSpace(rune(value)) {
			return written, ErrMalformedRequest
		}
	}
	return written, nil
}

func nextNonSpace(reader *bufio.Reader) (byte, error) {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if !unicode.IsSpace(rune(value)) {
			return value, nil
		}
	}
}

func readJSONString(reader *bufio.Reader, max int) (string, error) {
	start, err := nextNonSpace(reader)
	if err != nil || start != '"' {
		return "", ErrMalformedRequest
	}
	var value strings.Builder
	for value.Len() <= max {
		char, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		switch char {
		case '"':
			return value.String(), nil
		case '\\':
			return "", fmt.Errorf("escaped JSON keys are unsupported")
		default:
			if char < 0x20 {
				return "", ErrMalformedRequest
			}
			value.WriteByte(char)
		}
	}
	return "", ErrMalformedRequest
}

type jsonStringReader struct {
	reader *bufio.Reader
	ended  bool
	err    error
}

func (r *jsonStringReader) Read(buffer []byte) (int, error) {
	if r.ended {
		return 0, io.EOF
	}
	count := 0
	for count < len(buffer) {
		value, err := r.reader.ReadByte()
		if err != nil {
			r.err = err
			return count, io.EOF
		}
		switch value {
		case '"':
			r.ended = true
			return count, io.EOF
		case '\\':
			r.err = errors.New("escaped base64Source is invalid")
			return count, io.EOF
		default:
			if value < 0x20 {
				r.err = errors.New("control character in base64Source")
				return count, io.EOF
			}
			buffer[count] = value
			count++
		}
	}
	return count, nil
}
