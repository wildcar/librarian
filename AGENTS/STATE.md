# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

A multi-account librarian service (ADR-0004): isolated accounts keep documents of several
persons, an irregular stream of files and links is catalogued, users work with it through
an MCP server from any AI assistant.

## Now

- Document page opens the original («Открыть файл» next to each file, PDF at the document's page, served only while the hash matches); «Входящие» lists documents without a person and every open check, by kind; splits, doubted pages and assistant edits are settled there by buttons (catalog migration 19). Persons and date are edited on the document page, linked from person and date reasons. Owner checked the first part live 2026-10-22; the buttons deployed 2026-10-22.
- Server folders through the web (`/folder`): only inside the account's allowed roots; none set yet for `sergey`, and `librarian` may only traverse `/mnt/raid/document/Сережа`.
- Uploads through the web (`/upload`, ADR-0007): files become a labelled batch, the worker in `serve` takes them to search (auto up to $1 OCR estimate, else «Обработать» on `/batches`); `librarian run` does the same from the CLI. Checked live on a throwaway account.
- Assistants connect to `https://lib.wildcar.ru/mcp` by OAuth (ADR-0007): the letter names the assistant, «Ассистенты» lists and disconnects them, edits are signed with the assistant's name; checked live up to the authorization page, a real assistant not connected yet.
- Web interface live at `https://lib.wildcar.ru` (ADR-0007): login by a letter (`wildcar@mail.ru` → `sergey`), the tree with chosen levels, the document page. Letters leave the local Postfix; without SPF/DKIM and PTR Gmail will refuse them, mail.ru took them before.
- MCP `update_document`: an assistant refines persons, characteristics and date with the user's words as reason; marked until `librarian doc confirm | revert`.
- Manual refinement per document: `librarian doc set | show` (person, characteristics, date), kept by index over batches, rules and models.
- Batches (ADR-0006): the sample folder is batch 1, labelled ЕБВ — all 68 documents linked (60 by name before); characteristics per batch, searchable through MCP.
- Document date is the creation date (`librarian date`, run after `digest`, before `index`): on the sample 64 of 68 confirmed by a quote, 2 without one, 2 rejected and dated from the summary; `date_partial` for dates without a day or year; $0.008.
- An assistant worked through `lib.wildcar.ru` (2026-10-21); its feedback led to paging and absence search: `search_documents` returns `total`/`next_offset`, lists all without words, `missing: [date|person]`; `review` lists kinds of open checks.
- Service live at `https://lib.wildcar.ru/mcp` (systemd + Apache + Let's Encrypt, files in `deploy/`); checked over HTTPS: initialize, tools/list, list_persons, search, wrong token 401.
- Ubuntu server `homesrv` is dev and prod (toolchain, user `librarian`, `/var/lib/librarian`, env files). Sample run there (account `sergey`, from Nextcloud): 156 pages OCR $1.38, 1 flagged page; 68 documents, split $0.23, 0 reviews; digest $0.063, 1 flagged summary; 60 of 68 linked to the person.
- By name alone 60 of 68 linked: 4 forms with misread handwritten initials, 1 misspelt surname, 3 without a name; the batch label links them all.
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

- Mail: SPF, DKIM, DMARC and A `mail.wildcar.ru` published, Port25 check passes SPF and DKIM (2026-10-22); login e-mail wildcar@mail.ru bound to `sergey`, mail.ru accepts letters; Gmail accepts through the router's VPN exit 38.135.53.21 (PTR out.wildcar.ru); others get mail from 95.165.109.250 without PTR.
- Uploads from cloud links (ADR-0007).
- Characteristics read from documents by a model (kind of document, specialty, organisation kind), `source = model`, same table as batch characteristics.
- Owner: review `РАЗБИВКА.md` (68 documents) when convenient; corrections update the hand labels.
- `add_document` over MCP.
- Inbox: accept a flagged summary; characteristics from the page; a new person from the document page; `list_inbox` over MCP.
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
- Flagged pages of originals not yet split into documents are only in `stats`, not in «Входящие».
- `extract` processes originals one after another, pages in parallel; parallelism across originals if volume needs it.
- Go tests and linter beyond `go vet` / `gofmt`: no Go logic yet. `tools/ocrbench` has no tests (throwaway harness).
- Router provider `codex-oauth` disabled; GPT models via codex subscription not benchmarked.
