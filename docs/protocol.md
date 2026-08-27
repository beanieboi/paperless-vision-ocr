# Paperless-ngx / Azure Document Intelligence protocol

This document records the deliberately small compatibility surface implemented by
`paperless-vision-ocr`. It was researched on 2026-08-25 from:

- Paperless-ngx pull request [#10320](https://github.com/paperless-ngx/paperless-ngx/pull/10320).
- released Paperless-ngx **v3.0.5**, tag commit
  `d3da303113bab65f972ce5e1597a5ead49b43eb5`;
- the Paperless development branch at
  `492424f7f217a518a3778cd3f9d9023847ac8b60`;
- `azure-ai-documentintelligence` **1.0.2**, the version resolved by both the
  v3.0.5 and development `uv.lock` files. Paperless declares
  `azure-ai-documentintelligence>=1.0.2`.

The released and development parsers use the same successful request sequence.
The development parser now turns an SDK exception into a Paperless `ParseError`;
v3.0.5 logs it and returns no OCR result. This does not alter the wire protocol.

## Paperless calls

Paperless constructs `DocumentIntelligenceClient(endpoint,
AzureKeyCredential(api_key))`, then calls:

1. `begin_analyze_document(model_id="prebuilt-read", body=AnalyzeDocumentRequest(bytes_source=file.read()), output_content_format=DocumentContentFormat.TEXT, output=[AnalyzeOutputOption.PDF], content_type="application/json")`;
2. `poller.wait()`, followed by `poller.result()`;
3. `poller.details["operation_id"]`;
4. `client.get_analyze_result_pdf(model_id="prebuilt-read", result_id=...)`.

Paperless reads only `AnalyzeResult.content`. It streams all chunks from the PDF
iterator to its archive file. It does not read pages, words, polygons, tables,
styles, paragraphs, or document fields.

## API version and endpoint prefix

SDK 1.0.2 defaults to Azure API version **`2024-11-30`**. The generated client
unconditionally appends `/documentintelligence` to the configured Paperless
endpoint. Therefore `PAPERLESS_REMOTE_OCR_ENDPOINT=http://mac:8080` produces
requests below; operators must not append `/documentintelligence` themselves.

The SDK's `AnalyzeDocumentLROPoller.details` extracts the result ID with a regular
expression that requires an absolute URL containing `/documentintelligence/` and
takes the last path component before `?`. Returning only a relative
`Operation-Location`, or omitting that prefix, breaks Paperless after polling even
if polling itself succeeds.

## 1. Submit analysis

```http
POST /documentintelligence/documentModels/prebuilt-read:analyze?api-version=2024-11-30&outputContentFormat=text&output=pdf HTTP/1.1
Host: mac:8080
Content-Type: application/json
Accept: application/json
Ocp-Apim-Subscription-Key: dummy
User-Agent: azsdk-python-ai-documentintelligence/1.0.2 ...
x-ms-client-request-id: <SDK-generated UUID>

{"base64Source":"JVBERi0xLjQK..."}
```

`AnalyzeDocumentRequest(bytes_source=...)` is serialized as JSON containing a
base64 string; despite the name, Paperless does not send the PDF as
`application/octet-stream`. `Content-Length` and tracing headers can vary by
Azure Core/Python version. Authentication uses exactly
`Ocp-Apim-Subscription-Key` when an `AzureKeyCredential` is used.

The SDK accepts only **202 Accepted** for the initial call. The response body may
be empty. The operation URL must be absolute:

```http
HTTP/1.1 202 Accepted
Operation-Location: http://mac:8080/documentintelligence/documentModels/prebuilt-read/analyzeResults/550e8400-e29b-41d4-a716-446655440000?api-version=2024-11-30
Retry-After: 1
x-ms-request-id: 550e8400-e29b-41d4-a716-446655440000
```

`Retry-After` is optional. Without it the SDK 1.0.2 patched client polls every one
second. The service returns one second explicitly.

## 2. Poll analysis

The Azure Core long-running-operation poller sends `GET` to the exact absolute
`Operation-Location`. Normal polls return **200 OK**, `application/json`, and a
root-level `status`. Supported Azure casing is `notStarted`, `running`,
`succeeded`, and `failed`.

```http
GET /documentintelligence/documentModels/prebuilt-read/analyzeResults/550e8400-e29b-41d4-a716-446655440000?api-version=2024-11-30 HTTP/1.1
Ocp-Apim-Subscription-Key: dummy
```

Azure Core's generic LRO poll request does not currently add an `Accept` header;
the adapter does not require one.

Running response:

```json
{
  "status": "running",
  "createdDateTime": "2026-08-25T10:00:00Z",
  "lastUpdatedDateTime": "2026-08-25T10:00:01Z"
}
```

Minimal successful response accepted by SDK 1.0.2:

```json
{
  "status": "succeeded",
  "createdDateTime": "2026-08-25T10:00:00Z",
  "lastUpdatedDateTime": "2026-08-25T10:00:05Z",
  "analyzeResult": {
    "apiVersion": "2024-11-30",
    "modelId": "prebuilt-read",
    "stringIndexType": "textElements",
    "contentFormat": "text",
    "content": "Rechnung 12345\nGesamtbetrag 42,50 €",
    "pages": []
  }
}
```

Although Paperless only dereferences `content`, `apiVersion`, `modelId`,
`stringIndexType`, `content`, and `pages` are required fields in the SDK's
`AnalyzeResult` model, so this adapter supplies them.

Failed operations still return HTTP 200 and a failed LRO state:

```json
{
  "status": "failed",
  "createdDateTime": "2026-08-25T10:00:00Z",
  "lastUpdatedDateTime": "2026-08-25T10:00:02Z",
  "error": {
    "code": "OCROperationFailed",
    "message": "Local OCR processing failed."
  }
}
```

The Azure poller raises an exception for `failed`; the current development
Paperless parser converts this to `ParseError`.

## 3. Download the searchable PDF

After success Paperless obtains the ID from `poller.details`, not from the JSON,
and calls:

```http
GET /documentintelligence/documentModels/prebuilt-read/analyzeResults/550e8400-e29b-41d4-a716-446655440000/pdf?api-version=2024-11-30 HTTP/1.1
Accept: application/pdf
Ocp-Apim-Subscription-Key: dummy
```

The SDK accepts only **200 OK** and exposes the response as a byte iterator.

```http
HTTP/1.1 200 OK
Content-Type: application/pdf
Content-Length: 123456

%PDF-...
```

## Error behavior

Non-success HTTP responses use Azure's error envelope:

```json
{"error":{"code":"InvalidRequest","message":"A bounded public message."}}
```

The implemented subset returns 400 for malformed/invalid input or protocol
options, 401 for a wrong configured key, 404 for unknown/expired results, 409
when the PDF is requested before success, 413 for the upload limit, 415 for a
wrong content type, 503 for a full queue/readiness failure, and 500 for internal
failures. The Azure SDK retries retryable transport/status failures according to
Azure Core policy; the Paperless parser itself adds no queue-specific retry loop.

Only the three routes above are part of the Azure compatibility contract. There
is no URL-source input, OAuth, model administration, figures, tables, batch API,
or result deletion endpoint.
