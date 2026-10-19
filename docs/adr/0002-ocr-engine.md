# ADR-0002: OCR engine — vision model through the router, tesseract as a second reading

**Status:** Accepted (2026-10-18; primary model changed 2026-10-19)
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

Round 1 (2026-10-18) compared local engines and the router's existing vision models.
Round 2 (2026-10-19) added 12 newer OpenRouter models adopted into the router registry.

| Engine | Round | Total | Print | Tables | Handwriting | s/page | $/page |
|---|---|---|---|---|---|---|---|
| Gemini 4 Argon (router) | 2 | 99% | 100% | 100% | 94% | 16 | 0.0097 |
| Gemini 3.8 Flash (router) | 2 | 97% | 99% | 100% | 87% | 30 | 0.0186 |
| Claude Haiku 5.5 (router) | 2 | 96% | 100% | 100% | 74% | 9 | 0.0012 |
| Gemini 3.5 Flash Lite (router) | 2 | 94% | 99% | 100% | 70% | 5 | 0.0022 |
| Gemini 3.1 Flash Lite (router) | 1 | 93% | 99% | 100% | 74% | 3.6 | 0.0013 |
| GPT-5.6-sol (router) | 1 | 93% | 99% | 100% | 61% | 23 | 0.056 |
| Qwen3.8 Flash (router) | 2 | 92% | 99% | 100% | 59% | 32 | 0.0017 |
| Qwen3-VL 235B (router) | 2 | 91% | 99% | 100% | 50% | 20 | 0.0021 |
| GLM-5.3 Flash (router) | 1 | 90% | 98% | 100% | 52% | 63 | 0.0036 |
| MiMo v2.6 Flash (router) | 2 | 90% | 97% | 95% | 54% | 70 | 0.0016 |
| GPT-6 Luna (router) | 2 | 89% | 98% | 100% | 44% | 21 | 0.0015 |
| GPT-5.6-luna (router) | 1 | 88% | 98% | 100% | 41% | 13.5 | 0.0027 |
| Gemma 4 31B (router) | 2 | 86% | 93% | 87% | 54% | 33 | 0.0004 |
| surya 0.22 (local, CPU, llama.cpp) | 1 | 85% | 98% | 100% | 28% | 205 | 0 |
| Nemotron 3.5 Super VL (router) | 2 | 82% | 91% | 95% | 37% | 56 | 0.0021 |
| tesseract 5.4, rus tessdata_best (local) | 1 | 78% | 96% | 87% | 9% | 7 | 0 |
| PaddleOCR PP-OCRv5 eslav (local) | 1 | 68% | 83% | 68% | 13% | 53 | 0 |

Not completed: DeepSeek v4.1 Flash loops until the 131k output limit and returns
nothing; Mistral Small 4 hits provider rate limits (HTTP 429) even with 20 s pauses.

Findings that shape the decision:

- On print every engine but Paddle and Nemotron is at 96–100%. Handwriting separates them.
- Gemini 4 Argon is the only engine that read the hardest handwritten note and the
  rotated page coherently, and it costs a sixth of GPT-5.6-sol.
- Vision models hallucinate plausibly: a handwritten surname came back as many
  different real-looking surnames across models, and a cursive initial was misread by
  almost all of them, Argon included. Tesseract fails visibly, models fail invisibly.
- Aggregate scores hide dangerous errors: Claude Haiku 5.5 scored 96% but turned a
  handwritten benign diagnosis into a malignant one with metastases.
- surya is accurate on print but takes minutes per page on CPU and returned empty
  output under load. Paddle crashes on Windows (oneDNN, server detector).

## Decision

1. Primary page reading: a vision model through model-router-mcp, pinned by
   `model_id`: `google/gemini-4-argon` (since 2026-10-19; before that
   `google/gemini-3.1-flash-lite`). The model id is configuration, not code.
2. Second, independent reading by local tesseract on every page. On printed pages a
   large disagreement between the two texts flags the page for review.
3. People and dates taken from handwriting are never trusted as read: they are
   resolved against the people registry (aliases, birth date) and date rules. A
   document that does not resolve goes to `Входящие/`.
4. Every stored text keeps provenance: engine, model id, cost.

## Second reading: how the check works (2026-10-20)

Implemented in `internal/extract` (`librarian extract`, and `librarian verify` for
pages read before). Tesseract reads the page rendered at 300 dpi; only its confident
words count (confidence 80 or more, 4 letters or more). A page is judged only when it
has at least 20 such words, which keeps handwriting out. It is flagged when

- under 90% of the confident words occur in the Argon text (a large omission), or
- 3 or more consecutive confident words are missing (a dropped line).

Calibration on the 156 sample pages (112 judged as printed): the lowest real share was
0.92 and the longest run of missing words was 1, so no page was flagged. Every missing
word was a tesseract misreading («Минисжерство», «РО-интервалом» for PQ) except one:
Argon had silently corrected a typo of the original («отсуствие» → «отсутствие»)
despite the instruction to keep typos. Simulated omissions (lines removed from the
Argon text, all tesseract words of 4+ letters treated as confident, so the figures
are rough): one dropped long line is caught on about half of the pages, two lines on
60–80%, a fifth of the page on about 80%. The check catches dropped lines and large
gaps on printed pages; it does not catch a single wrong digit or a misread
handwritten name — those stay with the rules of decision 3.

Cost: no money, about 4 s of CPU per page.

## Consequences

- About $0.01 per page: the sample folder costs about $1.5 to read in full, an
  archive of 10,000 pages about $100. Quality on handwriting was judged worth 7× the
  cost of Flash Lite.
- The model is pinned by `model_id`, not chosen by tier: the router labels Argon
  `expensive`, so tier-based selection would never pick it, and silent substitution
  would change OCR quality without notice.
- Page images leave the machine: OpenRouter forwards them to the model vendor. The
  owner decided on 2026-10-18 that all pages, medical ones included, go through the
  router. Rejected for now: sending only pages tesseract could not read, or keeping
  medical sources local (surya on a GPU server).
- Tesseract and its `rus` model become a runtime dependency (see `AGENTS/ENV.md`);
  `extract --no-check` runs without it, `verify` gives skipped pages the check later.
- The bake-off is repeatable: new engines or models are added as another
  `run_*.py` (or another `--models` id) and scored against the same reference.

## Alternatives considered

- **Gemini 3.1 Flash Lite as primary** (the round-1 choice) — 7× cheaper, but 74% on
  handwriting against 94%, and it invents surnames where Argon reads them.
- **Two tiers: Flash Lite for print, Argon for handwriting** — cheaper at scale, but
  needs a reliable handwriting detector first. Revisit if volumes grow.
- **Claude Haiku 5.5** — cheap and strong on print, but hallucinated a malignant
  diagnosis from handwriting.
- **GPT-5.6-sol** — 6× the cost of Argon, worse on every category.
- **Tesseract only** — private and free, but 9% on handwriting and small errors in
  print (Roman numerals, Latin/Cyrillic look-alikes).
- **surya locally** — near model quality on print, but 3–5 min/page on CPU and
  fragile under load. Revisit on a GPU server.
- **PaddleOCR** — lowest score and unstable on Windows.
