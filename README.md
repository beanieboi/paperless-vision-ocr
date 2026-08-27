# paperless-macos-ocr

`paperless-macos-ocr` lets an unmodified Paperless-ngx installation use Apple
Vision for remote OCR. It impersonates only the three Azure Document
Intelligence endpoints Paperless actually calls.

This repository contains only the standalone OCR service. It does not modify
Paperless documents directly and does not include bulk reprocessing or library
migration tooling.

```text
Paperless-ngx
   ↓
Azure Document Intelligence compatibility API
   ↓
paperless-macos-ocr (one Go binary)
   ↓
mac-ocr
   ↓
Apple Vision
   ↓
searchable PDF
```

Documents remain on the Mac/LAN. There is no Azure account, cloud document
processing, Paperless plugin or Paperless fork, LLM, Python runtime, or
application-side Node.js runtime. The installation below builds `mac-ocr` from
the exact tested commit in the `beanieboi/mac-ocr` fork; Node and npm are not
needed.

## Tested versions

- macOS 26.6.2 (build 25G83, Apple silicon)
- Go 1.27.0
- `mac-ocr` `1.1.1-paperless.2`, fork commit
  [`516fdd0`](https://github.com/beanieboi/mac-ocr/commit/516fdd0f30f09084b9616156463228f9972d8618)
- Paperless-ngx 3.0.5
- `azure-ai-documentintelligence` 1.0.2 with `azure-core` 1.38.0

See [STATUS.md](STATUS.md) for the recorded end-to-end result and
[docs/protocol.md](docs/protocol.md) for the exact HTTP contract.

## Install

Clone the repository:

```sh
git clone https://github.com/beanieboi/paperless-mac-ocr.git
cd paperless-mac-ocr
```

Install the patched `mac-ocr` 1.1.1 universal binary:

```sh
./scripts/install-mac-ocr.sh
```

The installer fetches only fork commit
`516fdd0f30f09084b9616156463228f9972d8618`, verifies the checkout resolved to
that exact commit, and compiles a universal binary to
`$HOME/.local/bin/mac-ocr`. It requires Git and the Swift toolchain from Xcode or
Xcode Command Line Tools, but no Node or npm. The fork writes each recognized
line as one invisible PDF text run and fits that run to Vision's line box, so
spaces survive extraction without selectable text being clipped at the page
edge. See [docs/mac-ocr-patch.md](docs/mac-ocr-patch.md) for the diagnosis and
validation. Confirm the configured languages and provide the absolute path when
running the service:

```sh
$HOME/.local/bin/mac-ocr --version
$HOME/.local/bin/mac-ocr languages | grep -E '^(de-DE|en-US)$'
```

Build and run the service:

```sh
go build -trimpath -o paperless-macos-ocr ./cmd/paperless-macos-ocr
MAC_OCR_PATH="$HOME/.local/bin/mac-ocr" ./paperless-macos-ocr
```

Set `MAC_OCR_INSTALL_DIR` when invoking the installer to choose a different
destination directory. `make install-mac-ocr` is an equivalent convenience
target.

The server fails fast with a clear message if `mac-ocr` is missing, cannot run,
or does not support a configured language. The default listener is
`0.0.0.0:8080`; use `HOST=127.0.0.1` if Paperless runs on the same Mac.

## Paperless configuration

These are the current Paperless-ngx setting names:

```env
PAPERLESS_REMOTE_OCR_ENGINE=azureai
PAPERLESS_REMOTE_OCR_ENDPOINT=http://host.docker.internal:8080
PAPERLESS_REMOTE_OCR_API_KEY=dummy
```

For Paperless on another machine, replace `host.docker.internal` with the Mac's
LAN address. Do not append `/documentintelligence`; Microsoft's SDK appends it.

For authentication, set the same non-empty value on both sides:

```env
# paperless-macos-ocr
OCR_API_KEY=choose-a-long-random-value

# Paperless
PAPERLESS_REMOTE_OCR_API_KEY=choose-a-long-random-value
```

When `OCR_API_KEY` is empty, any supplied subscription key is accepted. This is
convenient on loopback, but a real key is recommended on a LAN. The protocol is
plain HTTP by default; put it behind a private TLS reverse proxy or encrypted
network when documents cross an untrusted network.

## Configuration

All configuration uses environment variables.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HOST` | `0.0.0.0` | Listen address |
| `PORT` | `8080` | Listen port |
| `OCR_LANGUAGES` | `de-DE,en-US` | Comma-separated BCP-47 hints |
| `OCR_STRATEGY` | `standard` | `standard` is reliable for ordinary scans; `auto` and `partitioned` are opt-in |
| `OCR_MAX_CONCURRENT_JOBS` | `1` | Bounded Apple Vision workers |
| `OCR_MAX_QUEUED_JOBS` | `20` | Pending-job capacity |
| `OCR_TIMEOUT_MINUTES` | `30` | Total timeout for both `mac-ocr` passes |
| `OCR_JOB_TTL_HOURS` | `24` | Result and file retention |
| `OCR_WORK_DIR` | OS temp dir + `paperless-macos-ocr` | Per-job storage |
| `OCR_API_KEY` | empty | Optional subscription key requirement |
| `OCR_MAX_UPLOAD_MB` | `100` | Decoded PDF upload limit |
| `MAC_OCR_PATH` | `mac-ocr` | Executable name or absolute path |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, or `ERROR` |

Each job uses a private directory:

```text
<OCR_WORK_DIR>/<random-uuid>/
    input.pdf
    output.pdf
```

Metadata is in memory. Completed artifacts are removed after the TTL; orphaned
UUID directories from a previous process are also removed after the TTL.

## Health probes

```sh
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8080/ready
```

`/health` reports process liveness. `/ready` checks that `mac-ocr` can execute and
that the work directory is writable. Probes do not perform OCR.

## Manual API test

Create the JSON/body exactly as Microsoft's SDK does:

```sh
base64 < scan.pdf | tr -d '\n' | jq -Rs '{base64Source:.}' > /tmp/analyze.json

curl -i \
  -H 'Content-Type: application/json' \
  -H 'Ocp-Apim-Subscription-Key: dummy' \
  --data-binary @/tmp/analyze.json \
  'http://127.0.0.1:8080/documentintelligence/documentModels/prebuilt-read:analyze?api-version=2024-11-30&outputContentFormat=text&output=pdf'
```

Poll the absolute URL from `Operation-Location`, then append `/pdf` before its
query string to download the searchable result. The official SDK harness under
`tests/azure-sdk/` is less error-prone for development; see
[its instructions](tests/azure-sdk/README.md).

## Tests

```sh
go test ./...
go vet ./...
go test -race ./...
```

The normal suite uses fake OCR and does not require Apple Vision. On macOS,
`TestMacOCRIntegration` runs automatically when `mac-ocr` and
`testdata/rechnung-image-only.pdf` are available. It checks OCR text, a valid
output PDF, and the selectable layer through `pdftotext` when installed.

The official Microsoft SDK check is isolated and optional:

```sh
uv venv tests/azure-sdk/.venv
uv pip install --python tests/azure-sdk/.venv/bin/python \
  -r tests/azure-sdk/requirements.txt

go run ./tests/compat-server
# in another terminal
tests/azure-sdk/.venv/bin/python tests/azure-sdk/compat.py \
  testdata/rechnung-image-only.pdf
```

Python is used only to load Microsoft's official client for compatibility
verification. It is not imported, embedded, or invoked by the service.

## launchd

An example LaunchAgent is at
[deploy/com.example.paperless-macos-ocr.plist](deploy/com.example.paperless-macos-ocr.plist).
Install the two binaries at the paths in that file (or edit the paths), copy it
to `~/Library/LaunchAgents/`, validate with `plutil -lint`, and load it with
`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.paperless-macos-ocr.plist`.

## Troubleshooting

- **Paperless cannot reach the Mac:** from its container, test
  `curl http://host.docker.internal:8080/health`. Use the Mac's LAN address on
  non-Docker or remote hosts, and allow the port through the macOS firewall.
- **Wrong endpoint:** configure only the origin, such as `http://mac:8080`.
  Adding `/documentintelligence` duplicates the SDK prefix.
- **`mac-ocr` missing:** use an absolute `MAC_OCR_PATH`; launchd has a smaller
  `PATH` than an interactive shell.
- **Unsupported language:** compare every `OCR_LANGUAGES` entry with
  `mac-ocr languages`. Startup intentionally fails instead of silently changing
  recognition behavior.
- **Words are fused when copying from an archive PDF:** reinstall `mac-ocr` with
  this repository's installer. Upstream 1.1.1 writes one independent PDF text
  run per word and leaves spacing to extractor heuristics; the pinned fork
  commit writes Vision's complete line string instead.
- **Polling never completes:** check structured logs for the job ID and
  `mac_ocr_exit_code`. The default timeout is 30 minutes.
- **Queue full:** increase `OCR_MAX_QUEUED_JOBS` only after checking disk and
  expected load. Azure Core retries some 503 responses automatically.
- **PDF missing or HTTP 409:** the job is still queued/running or failed. Poll
  the analyze result before requesting `/pdf`.
- **Encrypted/corrupt PDF:** `mac-ocr` fails the asynchronous operation. This
  service never accepts a password from the HTTP client.

## Design limits

`mac-ocr` currently needs two CLI invocations: JSONL OCR for
`analyzeResult.content`, then `searchable-pdf` for the positioned invisible text
layer. This duplicates recognition work but preserves a stable native CLI
boundary. The PDF pass intentionally does not use `--ocr-all-pages`, so its
default page-level existing-text detection remains active.

The default OCR strategy is `standard`. Upstream 1.1.1's `auto` strategy may
retain a few partial partition observations alongside the full-page pass on
dense scans. `auto` and `partitioned` remain available explicitly for documents
whose small text needs the extra passes.

## License

`paperless-macos-ocr` is available under the [MIT License](LICENSE).
