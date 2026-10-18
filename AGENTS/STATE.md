# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

Order a family document archive: ingest local and cloud files, analyse them with rules
and models (via model-router-mcp), file them into a shared structure.

## Now

- Filing design drafted: `docs/filing.md` (sources read-only, local tree of sidecars and PDF cuts, naming, splitting, virtual collections, people registry).
- ADR-0001 accepted: Go core, extractors chosen by measured quality.

## Next

- OCR bake-off on the 32 sample scans (none has a text layer): tesseract rus, vision models via router, a Python OCR (surya / PaddleOCR) as a candidate. Output: ADR-0002.
- First pipeline slice: walk a folder → sha256 + MIME → catalog → dedup report.

## Open questions

- Taxonomy: domain and leaf names (`Медицина/Приемы-назначения`, ...) — start from the sample folders?
- Person codes: three initials always? Namesakes and household documents (`Семья`)?
- PDF copies for cloud sources: `none` by default, or cut and keep locally too?
- Where the librarian runs: dev machine or the Ubuntu server.
- Interface: CLI only, or web UI / Telegram bot later.

## Deferred

- Tests and linter beyond `go vet` / `gofmt`: no code to test yet.
