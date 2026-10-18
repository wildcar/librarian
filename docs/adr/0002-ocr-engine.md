# ADR-0002: OCR engine — vision model through the router, tesseract as a second reading

**Status:** Accepted (2026-10-18)
**Date:** 2026-10-18
**Deciders:** wildcar
**Scope:** extraction stage; `tools/ocrbench/`

---

## Context

The first real source is a folder of 32 scanned medical PDFs (156 pages). None has
a text layer, so OCR quality decides everything downstream: dates, people, document
types, file names.

Bake-off (`tools/ocrbench/`): 14 pages of different layouts (printed protocols,
tables, invoices, forms with handwriting, handwritten notes, a rotated page). 135 key
facts were read by eye; an engine scores 1 for a verbatim hit after normalisation and
0.5 for a fuzzy one. Results and the reference stay outside the repo (personal data),
in the archive's `_эксперименты/ocr-2026-10/`.

| Engine | Total | Print | Tables | Handwriting | s/page | $/page |
|---|---|---|---|---|---|---|
| Gemini 3.1 Flash Lite (router) | 93% | 99% | 100% | 74% | 3.6 | 0.0013 |
| GPT-5.6-sol (router) | 93% | 99% | 100% | 61% | 23 | 0.056 |
| GLM-5.3 Flash (router) | 90% | 98% | 100% | 52% | 63 | 0.0036 |
| GPT-5.6-luna (router) | 88% | 98% | 100% | 41% | 13.5 | 0.0027 |
| surya 0.22 (local, CPU, llama.cpp) | 85% | 98% | 100% | 28% | 205 | 0 |
| tesseract 5.4, rus tessdata_best (local) | 78% | 96% | 87% | 9% | 7 | 0 |
| PaddleOCR PP-OCRv5 eslav (local) | 68% | 83% | 68% | 13% | 53 | 0 |

Findings that shape the decision:

- On print every engine but Paddle is at 96–99%. Handwriting separates them.
- Vision models hallucinate plausibly: a handwritten surname came back as five
  different real-looking surnames across models, and a cursive initial was misread by
  almost all of them. Tesseract fails visibly (garbage), models fail invisibly.
- The most expensive model was not the best on handwriting.
- surya is accurate on print but takes minutes per page on CPU and returned empty
  output under load. Paddle crashes on Windows (oneDNN, server detector).

## Decision

1. Primary page reading: a cheap vision model through model-router-mcp, today
   `google/gemini-3.1-flash-lite`. The model id is configuration, not code.
2. Second, independent reading by local tesseract on every page. On printed pages a
   large disagreement between the two texts flags the page for review.
3. People and dates taken from handwriting are never trusted as read: they are
   resolved against the people registry (aliases, birth date) and date rules. A
   document that does not resolve goes to `Входящие/`.
4. Every stored text keeps provenance: engine, model id, cost.

## Consequences

- About $0.0013 per page; the sample folder costs about $0.20 to read in full.
- Page images leave the machine: OpenRouter forwards them to the model vendor. The
  owner decided on 2026-10-18 that all pages, medical ones included, go through the
  router. Rejected for now: sending only pages tesseract could not read, or keeping
  medical sources local (surya on a GPU server).
- Tesseract and its `rus` model become a runtime dependency (see `AGENTS/ENV.md`).
- The bake-off is repeatable: new engines or models are added as another
  `run_*.py` and scored against the same reference.

## Alternatives considered

- **Tesseract only** — private and free, but 9% on handwriting and small errors in
  print (Roman numerals, Latin/Cyrillic look-alikes).
- **surya locally** — near model quality on print, but 3–5 min/page on CPU and
  fragile under load. Revisit on a GPU server.
- **PaddleOCR** — lowest score and unstable on Windows.
- **The strongest model for everything** — 40× the cost of Flash Lite with no gain on
  handwriting in this sample.
