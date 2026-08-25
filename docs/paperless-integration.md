# Paperless-ngx integration

## Recorded end-to-end test

This flow was executed successfully on 2026-08-25 with:

- native `paperless-macos-ocr` built with Go 1.26.2;
- native `mac-ocr` 1.1.1 on macOS 26.6.2;
- official `paperlessngx/paperless-ngx:3.0.5` Docker image, digest
  `sha256:65a4cabf0169ea7fbd90ab7bb28ba3f8b5909613635acda1a03ad606f34b456b`;
- Redis 8 in the isolated Docker network;
- `testdata/rechnung-image-only.pdf`, a one-page, 16,732-byte image-only PDF.

Paperless produced this exact Azure sequence:

1. JSON/base64 `POST` to `prebuilt-read:analyze`, receiving 202;
2. immediate poll returning `notStarted`/`running`;
3. one-second poll returning `succeeded` and OCR content;
4. PDF `GET` returning 200 and 39,055 bytes.

The service reported 0 ms queue time and 587 ms for the two real `mac-ocr`
passes. The complete Paperless consume task took 1.522 seconds. Paperless stored
the 16,732-byte original and a separate 39,055-byte archive. `pdftotext` on the
stored archive returned both `Rechnung 12345` and `Gesamtbetrag 42,50 €`.

Finally, Paperless's normal REST search was exercised:

```http
GET /api/documents/?query=Rechnung
```

It returned `count: 1`, document ID 1, the recognized content, and a ranked
search highlight for `Rechnung`. No Paperless source, plugin, or setting beyond
the supported remote-OCR variables was changed.

## Reproduce with Docker Paperless

Run the OCR service directly on the Mac:

```sh
HOST=0.0.0.0 \
PORT=8080 \
OCR_API_KEY=dummy \
MAC_OCR_PATH=/absolute/path/to/mac-ocr \
./paperless-macos-ocr
```

Add these variables to the Paperless webserver/worker environment (in the
official all-in-one image the same environment reaches its Celery worker):

```env
PAPERLESS_REMOTE_OCR_ENGINE=azureai
PAPERLESS_REMOTE_OCR_ENDPOINT=http://host.docker.internal:8080
PAPERLESS_REMOTE_OCR_API_KEY=dummy
```

Restart Paperless, confirm both reachability and readiness, then place
`testdata/rechnung-image-only.pdf` in the consume directory:

```sh
docker exec paperless curl -fsS http://host.docker.internal:8080/health
docker compose restart webserver
cp testdata/rechnung-image-only.pdf /path/to/paperless/consume/
docker compose logs -f webserver
```

Expected Paperless log milestones are an Azure POST with status 202, poll GETs
with status 200, the `/pdf` GET with `Content-Type: application/pdf`, and
`consumption finished`.

Verify the result in the UI by searching for `Rechnung`, or through the API:

```sh
curl -u 'admin:password' \
  'http://127.0.0.1:8000/api/documents/?query=Rechnung'
```

The result must have non-empty `content` and `archived_file_name`. Download the
archive in Paperless and confirm its text is selectable. If Poppler is installed:

```sh
pdftotext downloaded-archive.pdf - | grep -E 'Rechnung|12345'
```

On Docker Desktop and Colima, `host.docker.internal` normally resolves to the
Mac. On a remote Docker host, use the Mac's reachable LAN address instead. Do
not run the OCR service itself in Docker; Apple Vision requires native macOS.
