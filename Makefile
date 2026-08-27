.PHONY: build install-mac-ocr test race run fmt vet

GO ?= go

build:
	$(GO) build -trimpath -ldflags "-s -w -X main.version=$${VERSION:-dev}" -o paperless-vision-ocr ./cmd/paperless-vision-ocr

install-mac-ocr:
	./scripts/install-mac-ocr.sh

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

run:
	$(GO) run ./cmd/paperless-vision-ocr

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...
