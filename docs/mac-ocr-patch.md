# mac-ocr searchable-PDF whitespace patch

## Problem

Apple Vision returns correct line text, including spaces, and `mac-ocr`'s JSON
and JSONL output preserve it. In upstream `mac-ocr` 1.1.1, the searchable-PDF
writer instead splits each Vision observation into separately positioned word
runs. The `WordBox` values contain word text but deliberately exclude surrounding
whitespace.

That produces a PDF with no explicit separator between adjacent text runs. PDF
readers must infer spaces from geometry. The heuristic works for large, sparse
text but fuses many words in tightly spaced body text. Adding a trailing space
to every independent CoreText run was tested and was not sufficient: trailing
whitespace is not preserved consistently by PDF extractors.

## Patch

`patches/mac-ocr-1.1.1-searchable-pdf-spacing.patch` changes the invisible layer
to write one CoreText run per Vision observation using `observation.text` and the
observation's line bounding box. It retains an identity text matrix and natural
glyph metrics. This explicitly serializes Vision's whitespace without reviving
the older horizontal-scaling bug that caused inter-letter spaces.

The visible PDF page is untouched. Selection geometry is approximate at the line
level rather than positioned from every word box; individual words remain
searchable and selectable in reading order.

The patch also adds a generated dense-line regression test. It is applied only
to the pinned official `v1.1.1` source archive, whose SHA-256 is verified before
building.

## Validation

The patch was tested on macOS with the complete upstream suite:

```text
238 tests in 37 suites passed
```

On the affected two-page image-only scan, using identical languages and the
`standard` strategy:

| Extractor | Upstream 1.1.1 | Patched 1.1.1 |
| --- | ---: | ---: |
| Poppler `pdftotext` words | 247 | 460 |
| PDFKit words | 369 | 470 |
| Vision transcript words | 474 | 474 |

PDFKit's patched extraction retained 455 of the 474 Vision words in the same
sequence (96.0%). The remaining difference comes from PDF extraction order and
line geometry, not a loss of recognized spaces. Rasterizing both patched output
pages and comparing them to the source produced zero differing pixels.

`OCR_STRATEGY=standard` is now the adapter default. On this scan, upstream
`auto` accepted 11 partition observations in addition to the full-page pass;
some were partial overlaps. `auto` and `partitioned` remain available for
difficult small-text documents, but they are no longer the safe default.

## Rebuild

```sh
./scripts/install-mac-ocr.sh
```

The installer downloads the pinned source archive, verifies it, applies the
patch, stamps it as `1.1.1-paperless.1`, builds a universal native binary, and installs it to
`$HOME/.local/bin/mac-ocr` by default.
