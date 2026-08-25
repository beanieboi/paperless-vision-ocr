# Official Azure SDK compatibility harness

This directory is development-only. It reproduces Paperless-ngx v3.0.5's exact
Microsoft client calls with:

- `azure-ai-documentintelligence==1.0.2`;
- `azure-core==1.38.0`, its v3.0.5 lockfile resolution.

Python is not used by the production service.

From the repository root:

```sh
uv venv tests/azure-sdk/.venv
uv pip install --python tests/azure-sdk/.venv/bin/python \
  -r tests/azure-sdk/requirements.txt
go run ./tests/compat-server
```

In a second terminal:

```sh
tests/azure-sdk/.venv/bin/python tests/azure-sdk/compat.py \
  testdata/rechnung-image-only.pdf \
  --endpoint http://127.0.0.1:18080 \
  --output /tmp/azure-sdk-result.pdf
```

The harness uses `AnalyzeDocumentRequest(bytes_source=...)`,
`begin_analyze_document`, the SDK long-running-operation poller,
`poller.details["operation_id"]`, `poller.result().content`, and
`get_analyze_result_pdf`, matching Paperless. The compatibility server uses a
fake OCR runner so failures isolate HTTP/schema compatibility from Apple Vision.

For the strongest check, run the real service instead of `tests/compat-server`
and point `compat.py` at it. That real combination was also tested successfully;
see `STATUS.md`.
