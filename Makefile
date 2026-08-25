.PHONY: build test race run fmt vet

GO ?= go

build:
	$(GO) build -trimpath -ldflags "-s -w -X main.version=$${VERSION:-dev}" -o paperless-macos-ocr ./cmd/paperless-macos-ocr

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

run:
	$(GO) run ./cmd/paperless-macos-ocr

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...
