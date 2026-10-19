# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

A multi-account librarian service (ADR-0004): isolated accounts keep documents of several
persons, an irregular stream of files and links is catalogued, users work with it through
an MCP server from any AI assistant.

## Now

- First Go slice done: `librarian account create | ingest | dupes | stats`; per-account SQLite catalog; tested (unit + end-to-end on the sample folder).
- OCR bake-off done on 14 sample pages (`tools/ocrbench/`, ADR-0002 Accepted): tesseract 78% (9% on handwriting), Gemini 3.1 Flash Lite 93%.
- Report, reference facts and all engine outputs: `library/_эксперименты/ocr-2026-10/` (outside the repo, personal data).
- Tesseract text of all 156 sample pages is cached there for the splitting experiment.
- Round 2 (2026-10-19, 12 newer models): Gemini 4 Argon 99%, handwriting 94%, $0.0097/page — now the primary OCR model (ADR-0002).
- Service shape settled: ADR-0004 (accounts, persons, stream intake, MCP output; open questions resolved) and ADR-0005 (text layers, de-identified summaries by default).
- Layers experiment done (`tools/ocrbench/layers.py`, 68 documents): GPT-6 Luna and Gemini 4 Argon lost no facts and leaked nothing; Luna chosen ($0.001 per document). Report: `library/_эксперименты/layers-2026-10/`.
- Splitting experiment done (`tools/ocrbench/split.py`, ADR-0003 Accepted): Argon over the whole file 100%/100% on 27 files, also without handwritten dates; rules 93%/75% without them, decide weak. Report and proposed split of the folder: `library/_эксперименты/split-2026-10/`.

## Next

- Owner: review `РАЗБИВКА.md` (68 documents) when convenient; corrections update the hand labels.
- Next Go slice: page rendering and OCR through the router for new originals (ADR-0002), storing the verbatim text per page in the account.
- Persons within an account (code, aliases, birth date) — needed to resolve names in documents.
- MCP server skeleton with per-account credential and `list_persons` / `search_documents`.

## Open questions

- Non-contiguous documents and shuffled pages inside one scan: how to represent and fix them.
- Taxonomy: domain and leaf names — start from the sample folders?
- Person codes: three initials always? Namesakes and household documents (`Семья`)?
- PDF copies for cloud sources: `none` by default, or cut and keep locally too?
- Where the librarian runs: dev machine or the Ubuntu server (a GPU there would make surya viable).

## Deferred

- `go test -race`: needs cgo/gcc, run it on the Ubuntu server.
- Go tests and linter beyond `go vet` / `gofmt`: no Go logic yet. `tools/ocrbench` has no tests (throwaway harness).
- Router provider `codex-oauth` disabled; GPT models via codex subscription not benchmarked.
