# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

A multi-account librarian service (ADR-0004): isolated accounts keep documents of several
persons, an irregular stream of files and links is catalogued, users work with it through
an MCP server from any AI assistant.

## Now

- Document date is the creation date (`librarian date`, run after `digest`, before `index`): on the sample 64 of 68 confirmed by a quote, 2 without one, 2 rejected and dated from the summary; `date_partial` for dates without a day or year; $0.008.
- An assistant worked through `lib.wildcar.ru` (2026-10-21); its feedback led to paging and absence search: `search_documents` returns `total`/`next_offset`, lists all without words, `missing: [date|person]`; `review` lists kinds of open checks.
- Service live at `https://lib.wildcar.ru/mcp` (systemd + Apache + Let's Encrypt, files in `deploy/`); checked over HTTPS: initialize, tools/list, list_persons, search, wrong token 401.
- Ubuntu server `homesrv` is dev and prod (toolchain, user `librarian`, `/var/lib/librarian`, env files). Sample run there (account `sergey`, from Nextcloud): 156 pages OCR $1.38, 1 flagged page; 68 documents, split $0.23, 0 reviews; digest $0.063, 1 flagged summary; 60 of 68 linked to the person.
- Unlinked 8: 4 carry the name only in handwriting where OCR misread the initials (surname and birth year are right); 4 have no name at all.
- Fifth Go slice done: persons, `index`, tokens, `librarian serve` with list_persons / search_documents / get_document; checked with a real MCP client; 61 of 69 sample documents linked to ЕБВ.
- Fourth Go slice done: `librarian digest` / `show`; sample: 69 documents, 0 leaks, 1 flagged summary (a computed «28 дней»), $0.045.
- Third Go slice done: `librarian split` / `docs`; sample: 69 documents, 100% against hand labels, $0.24; two rules made weak after calibration.
- Tesseract second reading in the Go pipeline (ADR-0002): `extract` checks new OCR pages, `verify` backfills; 156 sample pages checked, 0 flagged (lowest share 0.92, longest missing run 1).
- Second Go slice done: `librarian extract` — text layer or OCR (Gemini 4 Argon via router) per page, stored in the account catalog. Sample account: 156 pages OCR'd for $1.39 in 10 min, 0 errors; facts score 98% vs 99% in the bench (the difference is one rotated handwritten page).
- First Go slice done: `librarian account create | ingest | dupes | stats`; per-account SQLite catalog; tested (unit + end-to-end on the sample folder).
- OCR bake-off done on 14 sample pages (`tools/ocrbench/`, ADR-0002 Accepted): tesseract 78% (9% on handwriting), Gemini 3.1 Flash Lite 93%.
- Report, reference facts and all engine outputs: `library/_эксперименты/ocr-2026-10/` (outside the repo, personal data).
- Tesseract text of all 156 sample pages is cached there for the splitting experiment.
- Round 2 (2026-10-19, 12 newer models): Gemini 4 Argon 99%, handwriting 94%, $0.0097/page — now the primary OCR model (ADR-0002).
- Service shape settled: ADR-0004 (accounts, persons, stream intake, MCP output; open questions resolved) and ADR-0005 (text layers, de-identified summaries by default).
- Layers experiment done (`tools/ocrbench/layers.py`, 68 documents): GPT-6 Luna and Gemini 4 Argon lost no facts and leaked nothing; Luna chosen ($0.001 per document). Report: `library/_эксперименты/layers-2026-10/`.
- Splitting experiment done (`tools/ocrbench/split.py`, ADR-0003 Accepted): Argon over the whole file 100%/100% on 27 files, also without handwritten dates; rules 93%/75% without them, decide weak. Report and proposed split of the folder: `library/_эксперименты/split-2026-10/`.

## Next

- Open characteristics and tags per document (differ by kind: a photo vs a referral), searchable by presence/absence; library stays general-purpose (MEMORY).
- Owner: review `РАЗБИВКА.md` (68 documents) when convenient; corrections update the hand labels.
- One command that runs the whole pipeline for new files (ingest → extract → split → digest → date → index), and `add_document` over MCP.
- Person matching: surname plus birth year near each other as a weak signal (covers the 4 misread handwritten names); the rest go to the inbox for manual linking.
- Inbox for the owner: unlinked documents, flagged summaries, split reviews.
- Persons within an account (code, aliases, birth date) — needed to resolve names in documents.
- MCP server skeleton with per-account credential and `list_persons` / `search_documents`.

## Open questions

- Non-contiguous documents and shuffled pages inside one scan: how to represent and fix them.
- Taxonomy: domain and leaf names — start from the sample folders?
- Person codes: three initials always? Namesakes and household documents (`Семья`)?
- PDF copies for cloud sources: `none` by default, or cut and keep locally too?

## Deferred

- Search ranking: bm25 favours short texts that mention a word; tune on real questions.
- Date sanity: device clocks (an ECG dated 1970), dates before the person's birth.
- Flagged summaries and split reviews wait for the inbox.
- Split review files have no inbox yet; `stats` lists them.
- Flagged pages are listed by `stats`; they go to the inbox once the inbox exists.
- `extract` processes originals one after another, pages in parallel; parallelism across originals if volume needs it.
- Go tests and linter beyond `go vet` / `gofmt`: no Go logic yet. `tools/ocrbench` has no tests (throwaway harness).
- Router provider `codex-oauth` disabled; GPT models via codex subscription not benchmarked.
