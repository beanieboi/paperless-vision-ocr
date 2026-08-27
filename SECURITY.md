# Security policy

## Reporting a vulnerability

Please use GitHub's private security-advisory flow for this repository instead
of opening a public issue. Do not attach real documents, OCR output, API keys,
or other sensitive Paperless data to a report. A minimal synthetic PDF and
redacted logs are preferred.

## Deployment model

`paperless-macos-ocr` is intended for a trusted host and private network. It
supports an optional shared API key but does not provide TLS, Azure identity,
or tenant isolation. Use `HOST=127.0.0.1`, an encrypted private network, or a
TLS reverse proxy when the service is not confined to a trusted LAN.

Only the current `main` branch is supported with security fixes.
