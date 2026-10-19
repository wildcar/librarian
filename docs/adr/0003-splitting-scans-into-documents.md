# ADR-0003: Splitting multi-document scans — whole-file model call, rules as a check

**Status:** Accepted (2026-10-19)
**Date:** 2026-10-19
**Deciders:** wildcar
**Scope:** splitting stage; `tools/ocrbench/split.py`

---

## Context

One scanned PDF often holds several paper documents back to back: in the sample
folder 32 PDFs (156 pages) hold 68 documents, and one 13-page scan holds 12. The
librarian must decide, for every boundary between consecutive pages, whether the next
page starts a new document.

Experiment (`tools/ocrbench/split.py`, results in the archive's
`_эксперименты/split-2026-10/`): 124 boundaries labelled by hand (36 true cuts, 3 left
unscored as ambiguous), page text from Gemini 4 Argon (ADR-0002). Convention:
ultrasound/ECG printouts, specimen sticker sheets and patient diaries belong to the
protocol of their examination.

| Method | Precision | Recall | Files without error | Cost for the folder |
|---|---|---|---|---|
| Chat over the whole file, Gemini 4 Argon | 100% | 100% | 27 of 27 | $0.21 |
| Rules (Argon text) | 94% | 94% | 23 of 27 | 0 |
| Rules, weak cases sent to `decide` | 90% | 100% | 23 of 27 | ~$0.002 |
| Chat over the whole file, Gemini 3.1 Flash Lite | 81% | 97% | 18 of 27 | $0.016 |
| `decide` on every boundary | 78% | 100% | 17 of 27 | $0.011 |
| Rules (tesseract text) | 57% | 47% | 15 of 27 | 0 |

Findings:

- The owner writes a date by hand on the first page of almost every document (58 of
  68). It is the strongest single signal, and only a good OCR sees it.
- The rules were tuned once on the same data (56% → 94% precision after fixing three
  general mistakes and a regex bug), so their figure is optimistic.
- `decide` is well calibrated at the extremes and close to a coin toss between 0.5 and
  0.8; no threshold fixes that.
- Documents are not always contiguous: an ECG tape can be separated from its protocol
  by another document, and a report's pages can be shuffled.

## Decision

1. Primary splitter: one `chat` call per file through the router with
   `google/gemini-4-argon`, given the OCR text of all pages and the filing conventions,
   returning the first page of each document.
2. The text rules run independently. Where a strong rule disagrees with the model,
   the file goes to `Входящие/` for the owner to confirm.
3. `decide` is not used for splitting.
4. Reassembling non-contiguous documents and restoring page order are a separate,
   later step.

## Consequences

- About $0.0013 per page on top of OCR (ADR-0002); the whole pipeline so far is about
  $0.011 per page.
- Long files need windowing: the experiment sent at most 2,500 characters per page and
  files of up to 15 pages.
- The rules stay in the codebase as a cheap, explainable cross-check, and as the
  fallback when the router is unavailable.
- Every split carries provenance: model id or rule name, and whether the two agreed.

## Alternatives considered

- **Rules only** — free and explainable, 94% here, but tuned on this sample and
  dependent on clinic letterheads.
- **Rules plus `decide` on weak boundaries** — cheapest model-assisted option, but the
  uncertain band is where `decide` fails.
- **A cheaper model for the whole-file call** — Flash Lite erred in 9 of 27 files.
