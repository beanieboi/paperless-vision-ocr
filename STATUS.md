# Status

Status date: **2026-08-27**

## Summary

The primary end-to-end goal works:

```text
image-only PDF
  → unmodified Paperless-ngx 3.0.5
  → Azure Document Intelligence SDK 1.0.2 HTTP sequence
  → native Go paperless-vision-ocr service
  → native mac-ocr 1.1.1 / Apple Vision
  → searchable PDF and OCR text
  → Paperless archive and normal full-text search hit
```

Paperless was not patched and no Azure endpoint was contacted.

## Exact versions tested

- Paperless-ngx: **v3.0.5**, release tag commit
  `d3da303113bab65f972ce5e1597a5ead49b43eb5`
- Paperless development source inspected at
  `492424f7f217a518a3778cd3f9d9023847ac8b60`
- Paperless container: `paperlessngx/paperless-ngx:3.0.5`, digest
  `sha256:65a4cabf0169ea7fbd90ab7bb28ba3f8b5909613635acda1a03ad606f34b456b`
- Azure SDK: `azure-ai-documentintelligence==1.0.2`
- Azure Core: `azure-core==1.38.0` (Paperless v3.0.5 lockfile resolution)
- `mac-ocr`: **1.1.1-paperless.2**, universal Mach-O built from pinned
  `beanieboi/mac-ocr` commit `516fdd0f30f09084b9616156463228f9972d8618`
- Go: **go1.27.0 darwin/arm64**
- macOS: **26.6.2**, build **25G83**, Apple silicon
- Poppler `pdftotext`: present and used for text-layer validation

## What works

- Exact `prebuilt-read` analyze, polling, and generated-PDF routes for Azure API
  `2024-11-30`.
- The SDK's JSON/base64 request body is decoded directly to a `0600` file rather
  than buffered as a PDF in Go memory.
- Absolute `Operation-Location` URLs satisfy SDK 1.0.2's strict
  `/documentintelligence/` operation-ID parser.
- Minimal `AnalyzeResult` schema accepted by Microsoft's official SDK.
- Asynchronous `notStarted`, `running`, `succeeded`, and `failed` job states.
- Bounded queue and bounded Apple Vision worker concurrency.
- Direct, context-aware `mac-ocr` execution without a shell.
- Real `mac-ocr` JSONL parsing and searchable-PDF generation with repeatable
  `-l` flags and `--ocr-strategy`.
- Explicit whitespace in searchable PDFs: the pinned patch writes one invisible
  text run per Vision line instead of separator-free per-word runs.
- Long invisible lines are fitted to their Vision bounding boxes so selectable
  trailing text cannot run beyond the page and be clipped by PDF readers.
- Existing searchable pages keep `mac-ocr`'s default skip behavior; the service
  never passes `--ocr-all-pages`.
- Total subprocess timeout and cancellation on service shutdown.
- Streaming PDF download with content type and length.
- Optional constant-time API-key comparison.
- Upload limits, PDF signature validation, private UUID directories, TTL cleanup,
  orphan cleanup, and expired-result tombstones.
- `/health`, executable/work-directory `/ready`, structured `slog` output,
  startup diagnostics, signal handling, and bounded HTTP shutdown.
- Azure-shaped bounded errors without subprocess stderr or OCR content exposure.
- Unit/integration coverage for configuration, repository concurrency and expiry,
  queue backpressure, worker success/failure/timeout, cleanup, authentication,
  upload validation, result states, expiry, and PDF serving.
- A validated LaunchAgent example and operator documentation.

## Searchable-PDF whitespace fix

Upstream `mac-ocr` 1.1.1's JSONL transcript was correct, but its searchable-PDF
writer omitted whitespace between independently positioned word runs. On a
real two-page image-only scan, the patch improved extraction from 247 to 460
words with Poppler and from 369 to 470 words with PDFKit. Vision recognized 474
words; PDFKit retained 455 in the same sequence (96.0%). Raster comparisons of
both pages found zero visible pixel differences.

The fork's full suite passed with 252 tests in 39 suites after the whitespace
change. The subsequent width-fitting regression and the searchable-PDF suite
pass on `1.1.1-paperless.2`. The installer fetches and verifies the pinned fork
commit and builds an arm64/x86_64 universal binary.

The adapter now defaults to `OCR_STRATEGY=standard`. Upstream `auto` accepted 11
extra partition observations on the affected scan, including partial overlaps;
operators can still opt into `auto` or `partitioned` for difficult small text.
See [docs/mac-ocr-patch.md](docs/mac-ocr-patch.md).

## Compatibility results

### Official SDK with fake OCR

`tests/azure-sdk/compat.py` completed the exact Paperless method sequence against
the Go fake-runner server. The SDK accepted the response schema, extracted
operation ID `186c0772-dc61-4f99-8df6-4413fc795bbd`, returned
`AnalyzeResult.content`, and streamed the 10,001,357-byte input back through the
PDF result endpoint. This isolates and proves protocol/deserialization behavior.

### Official SDK with real Apple Vision

The same SDK harness then targeted the production server with real `mac-ocr`.
For `testdata/rechnung-image-only.pdf` it returned:

```text
Rechnung 12345
Gesamtbetrag 42,50 €
```

The 16,732-byte image-only PDF became a distinct 39,055-byte searchable PDF.
`pdftotext` extracted both expected strings. The initial recorded run took 574
ms end to end inside the service.

After deploying the whitespace patch, a final Azure-compatible POST/poll/PDF
smoke test against the Mac mini produced the same recognized text and a
38,108-byte searchable PDF. `pdftotext` again extracted both expected lines.
The service recorded 7 ms queue duration and 1,631 ms OCR time on this
first job after restart. The deployed service identifies itself as commit
`a2c6661`, uses `mac-ocr 1.1.1-paperless.2`, and reports
`ocr_strategy=standard` and ready health.

After deploying the line-width fix, the same Azure-compatible smoke sequence
again succeeded. It produced a 38,105-byte searchable PDF whose two fixture
lines were extracted correctly; the service recorded 7 ms queue time and
1,578 ms OCR time. The deployed universal binary has SHA-256
`0ad042d00f0567b3da313a9a98063b2173c95543f721d477fcab84b761709b14`.

After the project rename, the production Mac mini was redeployed from adapter
commit `de46b22` under the LaunchAgent label `com.paperless-vision-ocr`. The
binary, cache directory, plist, and log paths all use the
`paperless-vision-ocr` name; the previous cache and diagnostic artifacts were
preserved under renamed paths. The old launchd identity and old-named artifact
filenames are absent. The deployed arm64 adapter binary has SHA-256
`9436213c14ff4c765b9f925d70609de94a734c76a933534166bdc5442581a970`
and uses `mac-ocr 1.1.1-paperless.2`.

A full POST/poll/PDF production smoke test after that rename returned the two
expected fixture lines and a 38,105-byte `application/pdf` result. Its SHA-256
was `2ee2d1252e0bdcd28d740fc5502862c53f5809272fca7a68b40a18a82b90c7f2`,
and `pdftotext` extracted both lines. The service recorded 7 ms queue time and
1,646 ms OCR time; `/health` and `/ready` both passed.

A subsequent physical scanner run completed the full production path into
Paperless document 1112. The downloaded 9,636,625-byte original was image-only;
the 9,652,099-byte archive contained 56 selectable lines. PDFKit found no line
crossing either page boundary, and all 226 normalized tokens in Paperless's
indexed OCR content matched the archive's embedded tokens in the same order.
Raster comparisons found zero differing pixels on both pages.

### Paperless-ngx Docker

An isolated Paperless-ngx 3.0.5 deployment with Redis 8 submitted the same
fixture to the service through `host.docker.internal`. Its logs recorded:

- POST 202;
- poll GET 200;
- second poll GET 200 after one second;
- searchable PDF GET 200 with `application/pdf` and 39,055 bytes;
- successful document consumption, archive storage, and indexing.

The service recorded 0 ms queue time and 587 ms OCR time. Paperless's full
consume task took 1.522 seconds. Paperless stored both the 16,732-byte original
and a 39,055-byte archive. The archive passed `pdftotext` validation.

Finally, Paperless's regular
`GET /api/documents/?query=Rechnung` endpoint returned one ranked result with
the recognized content and search highlight. This validates search, not merely
database storage.

## What does not work / intentional scope

- Only PDF `base64Source` requests from Paperless's current SDK call are
  supported. Azure URL sources, images, batch APIs, figures, tables, model
  administration, OAuth, and alternate API versions are intentionally absent.
- Job metadata is in memory. A restart loses API access to prior jobs and cancels
  active jobs; orphaned files are cleaned after the TTL.
- The service provides HTTP, not built-in TLS. Deployment security is left to a
  private network or reverse proxy.
- Encrypted PDFs cannot receive a password through this API and fail safely.
- The adapter does not independently count PDF pages or deeply validate PDF
  structure before invoking `mac-ocr`; malformed/zero-page documents become
  failed asynchronous operations after the cheap signature check.
- `mac-ocr` is run twice: once for streaming JSONL text and once for the
  searchable PDF. This is correct but duplicates Vision recognition work.
- OCR text remains in in-memory job state until expiry because Paperless needs it
  in `AnalyzeResult.content`. Document text is never written to service logs.
- There are no Prometheus metrics; timings and sizes are available as structured
  log fields.

## Build and run

```sh
./scripts/install-mac-ocr.sh
go build -trimpath -o paperless-vision-ocr ./cmd/paperless-vision-ocr
MAC_OCR_PATH="$HOME/.local/bin/mac-ocr" OCR_API_KEY=dummy ./paperless-vision-ocr
```

Paperless configuration:

```env
PAPERLESS_REMOTE_OCR_ENGINE=azureai
PAPERLESS_REMOTE_OCR_ENDPOINT=http://host.docker.internal:8080
PAPERLESS_REMOTE_OCR_API_KEY=dummy
```

Do not add `/documentintelligence` to the endpoint.

## Test commands and final result

Executed successfully:

```sh
gofmt -w ./cmd ./internal ./tests/compat-server  # equivalent file expansion used
go vet ./...
go test ./...
go test -race ./...
go build -trimpath ./cmd/paperless-vision-ocr
plutil -lint deploy/com.example.paperless-vision-ocr.plist
swift test --filter SearchablePDFTests  # in the pinned mac-ocr fork
```

The real `TestMacOCRIntegration` also passed with `mac-ocr` on `PATH`; it was not
skipped. Normal tests pass without `mac-ocr` and skip only that native integration
test when the executable is unavailable.

The fork's focused searchable-PDF suite passes on commit `516fdd0`, including
the explicit-spacing and line-width regressions. Two attempts to rerun all fork
tests under Xcode 17F113 stalled in the pre-existing `TestSupport.run` pipe wait
(`CustomWordsTests` once and `PDFDPITests` once); each implicated suite passes
when isolated, and neither exercises the changed line-fitting function. This is
a test-harness deadlock, not a recorded product-test failure.

## Remaining TODOs

1. Use a future supported `mac-ocr` API that emits OCR text and a searchable PDF
   from one recognition pass, if such a stable CLI contract becomes available.
2. Add optional persistent job metadata only if restart-surviving result URLs are
   operationally required.
3. Re-check the three-route contract when Paperless changes its Azure SDK pin or
   API version.

## Protocol assumptions

- Paperless continues to use `prebuilt-read`, text content format, PDF output,
  JSON `base64Source`, and API `2024-11-30` through SDK 1.0.2.
- The configured Paperless endpoint is an origin without the SDK-owned
  `/documentintelligence` suffix.
- Paperless reads only `AnalyzeResult.content` and the generated PDF byte stream.
- Pinned fork commit `516fdd0` retains the CLI options and JSONL `text` field
  used by the service; its relevant behavior change is searchable-PDF
  text-layer serialization.

The full route and schema evidence is in [docs/protocol.md](docs/protocol.md), and
the reproducible Paperless procedure is in
[docs/paperless-integration.md](docs/paperless-integration.md).
