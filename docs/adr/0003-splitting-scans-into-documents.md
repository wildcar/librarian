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

Rerun without the handwritten date (2026-10-19). The owner's handwritten date on the
first page of each document (58 of 68 here) was a habit for this batch only, so the
dates were removed from the page text and from the prompts (`--strip-hand-date`):

| Method | Precision | Recall | Files without error | Cost for the folder |
|---|---|---|---|---|
| Chat over the whole file, Gemini 4 Argon | 100% | 100% | 27 of 27 | $0.21 |
| Chat over the whole file, Gemini 3.1 Flash Lite | 90% | 100% | 23 of 27 | $0.024 |
| Rules (Argon text) | 93% | 75% | 19 of 27 | 0 |
| Rules, weak cases sent to `decide` | 88% | 83% | 19 of 27 | ~$0.002 |
| `decide` on every boundary | 80% | 100% | 18 of 27 | $0.011 |

Findings:

- The whole-file Argon call does not depend on the handwritten date: 100% either way.
- The rules do: without the date their recall drops from 94% to 75%. They remain a
  useful check against extra cuts (precision 93%), much less against missed ones.
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
   the file goes to `Входящие/` for the owner to confirm. The rules must not rely on
   handwritten dates.
3. `decide` is not used for splitting.
4. Reassembling non-contiguous documents and restoring page order are a separate,
   later step.

## In the service (2026-10-20)

`internal/split` (`librarian split`, `librarian docs`). One `chat` call per file with
the no-date prompt; files longer than 30 pages go in windows and the seams are decided
by the rules. A strong rule that contradicts the model sends the file to review; the
model's cut is kept meanwhile.

Calibration of the rules on the Go pipeline's own page text (`TestCalibrateRules`,
run with the catalog and the hand labels): five rules were right every time and stay
strong — running page number, printout after printout, protocol after its printouts,
page starting mid-document, title after an ending. Two were not and became weak: the
same case or contract number (4 right, 3 wrong) and the same letterhead (11 right, 3
wrong) — several payment details under one contract, or several recommendations on
one clinic's form, are separate documents. With them strong, 4 of 27 files went to
review for nothing.

Result on the sample: 69 documents, precision and recall 100% on every unambiguous
boundary against the hand labels, no file sent to review, $0.24 for the folder.

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
