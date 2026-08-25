# mac-ocr searchable-PDF text-layer patches

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

## Patches

Fork commit
[`b26091f`](https://github.com/beanieboi/mac-ocr/commit/b26091f7ef0d5390c5c586c79f1cb06113223a50)
changes the invisible layer to write one CoreText run per Vision observation
using `observation.text` and the observation's line bounding box. It retains an
identity text matrix and natural glyph metrics. This explicitly serializes
Vision's whitespace without reviving the older horizontal-scaling bug that
caused inter-letter spaces.

The visible PDF page is untouched. Selection geometry is approximate at the line
level rather than positioned from every word box; individual words remain
searchable and selectable in reading order.

The commit also adds a generated dense-line regression test.

That line-level change exposed a second problem on long scanned lines. A system
font at the detected line height can be wider than the original document font.
The invisible glyph run then extends beyond its Vision bounding box and, near a
page edge, beyond the page itself. PDF readers clip the trailing selectable text
even though Vision recognized the entire line.

Fork commit
[`516fdd0`](https://github.com/beanieboi/mac-ocr/commit/516fdd0f30f09084b9616156463228f9972d8618)
measures the CoreText line and uniformly reduces its font size only when it is
wider than Vision's line box. It deliberately does not use a horizontal text
matrix, preserving the first fix's clean word extraction. It adds a deterministic
width-fitting regression and stamps fork builds as `1.1.1-paperless.2`.

## Validation

The commit was tested on macOS against the fork's current `develop` suite:

```text
252 tests in 39 suites passed
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

On a second affected two-page scan, PDFKit reported seven line-selection bounds
past the 595.32-point page edge with `1.1.1-paperless.1`; the furthest ended at
604.07 points. With `1.1.1-paperless.2`, no line crossed the page edge and the
furthest ended at 570.64 points. The affected page remained pixel-identical.
The focused spacing and width-fitting regression tests passed against the new
commit.

`OCR_STRATEGY=standard` is now the adapter default. On this scan, upstream
`auto` accepted 11 partition observations in addition to the full-page pass;
some were partial overlaps. `auto` and `partitioned` remain available for
difficult small-text documents, but they are no longer the safe default.

## Rebuild

```sh
./scripts/install-mac-ocr.sh
```

The installer fetches and verifies the exact fork commit, builds a universal
native binary, and installs it to `$HOME/.local/bin/mac-ocr` by default.
