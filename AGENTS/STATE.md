# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

Order a family document archive: ingest local and cloud files, analyse them with rules
and models (via model-router-mcp), file them into a shared structure.

## Now

- OCR bake-off done on 14 sample pages (`tools/ocrbench/`, ADR-0002 Accepted): Gemini 3.1 Flash Lite via router 93% at $0.0013/page, tesseract 78% (9% on handwriting).
- Report, reference facts and all engine outputs: `library/_эксперименты/ocr-2026-10/` (outside the repo, personal data).
- Tesseract text of all 156 sample pages is cached there for the splitting experiment.

## Next

- Splitting experiment: find document boundaries in multi-page scans (rules on tesseract text plus router `decide`).
- First pipeline slice in Go: walk a folder → sha256 + MIME → catalog → dedup report.
- People registry draft (code, aliases, birth date) — needed to resolve handwritten names.

## Open questions

- Taxonomy: domain and leaf names — start from the sample folders?
- Person codes: three initials always? Namesakes and household documents (`Семья`)?
- PDF copies for cloud sources: `none` by default, or cut and keep locally too?
- Where the librarian runs: dev machine or the Ubuntu server (a GPU there would make surya viable).

## Deferred

- Go tests and linter beyond `go vet` / `gofmt`: no Go logic yet. `tools/ocrbench` has no tests (throwaway harness).
- Router provider `codex-oauth` disabled; GPT models via codex subscription not benchmarked.
