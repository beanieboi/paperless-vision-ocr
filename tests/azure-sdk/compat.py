#!/usr/bin/env python3
"""Exercise the exact Azure SDK call sequence used by Paperless-ngx.

This is a development-only compatibility harness. Python and the Azure SDK are
not runtime dependencies of paperless-vision-ocr.
"""

import argparse
from pathlib import Path

from azure.ai.documentintelligence import DocumentIntelligenceClient
from azure.ai.documentintelligence.models import AnalyzeDocumentRequest
from azure.ai.documentintelligence.models import AnalyzeOutputOption
from azure.ai.documentintelligence.models import DocumentContentFormat
from azure.core.credentials import AzureKeyCredential


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path)
    parser.add_argument("--endpoint", default="http://127.0.0.1:18080")
    parser.add_argument("--key", default="dummy")
    parser.add_argument("--output", type=Path, default=Path("/tmp/azure-sdk-result.pdf"))
    args = parser.parse_args()

    client = DocumentIntelligenceClient(
        endpoint=args.endpoint,
        credential=AzureKeyCredential(args.key),
        polling_interval=0.1,
    )
    try:
        request = AnalyzeDocumentRequest(bytes_source=args.input.read_bytes())
        poller = client.begin_analyze_document(
            model_id="prebuilt-read",
            body=request,
            output_content_format=DocumentContentFormat.TEXT,
            output=[AnalyzeOutputOption.PDF],
            content_type="application/json",
        )
        poller.wait()
        result_id = poller.details["operation_id"]
        result = poller.result()
        with args.output.open("wb") as output:
            for chunk in client.get_analyze_result_pdf(
                model_id="prebuilt-read",
                result_id=result_id,
            ):
                output.write(chunk)
        if not result.content:
            raise RuntimeError("SDK returned empty AnalyzeResult.content")
        if not args.output.read_bytes().startswith(b"%PDF-"):
            raise RuntimeError("SDK PDF result is not a PDF")
        print(f"operation_id={result_id}")
        print(f"content={result.content}")
        print(f"pdf={args.output} ({args.output.stat().st_size} bytes)")
    finally:
        client.close()


if __name__ == "__main__":
    main()
