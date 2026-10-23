# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

A multi-account librarian service (ADR-0004): isolated accounts keep documents of several
persons, an irregular stream of files and links is catalogued, users work with it through
an MCP server from any AI assistant.

## Now

- Live at `https://lib.wildcar.ru` on `homesrv` (dev and prod): web interface and MCP at `/mcp`; deployed after every change (`deploy/README.md`).
- MCP tools: `list_persons`, `search_documents` (paging, `missing`, `attrs`, `batch`), `get_document` (summary, essential, verbatim), `update_document` (assistant edits, marked until the owner accepts), `add_document` (new 2026-10-23: a file in base64, a text or a cloud link; batch records the assistant and the user's words; not yet tried by a real assistant).
- Assistants connect by OAuth with a letter (ADR-0007) or the account token; one assistant worked through the service 2026-10-21.
- Web: login by a letter, document tree with chosen levels, document page with edits and «Открыть файл», «Входящие» where every check can be closed, «Загрузить» (files, cloud links), «Папка» (server folders inside allowed roots; none set for `sergey`), «Пачки», «Ассистенты».
- Intake: folders, uploads, cloud links (Yandex Disk, Nextcloud, Google Drive; one real link worked 2026-10-23), MCP. Formats: PDF, images, plain text, .docx/.xlsx, .doc (cp1251 Word 95 fixed 2026-10-23, migration 22), .xls.
- Pipeline (the worker in `serve`, or `librarian run`): extract (text layer or Gemini 4 Argon OCR + tesseract check) → split → digest (essential text, de-identified summary by GPT-6 Luna) → date → describe (title, вид / специальность / организация as `source = model`, guesses marked) → index.
- Account `sergey`: the sample folder (68 documents, batch 1 «ЕБВ») plus the owner's uploads; persons ЕБВ (Папа) and ЕСБ (Я).
- Mail: SPF, DKIM, DMARC published; mail.ru and Gmail accept; other domains get mail from 95.165.109.250 without PTR (ENV.md).

## Next

- Owner: ask an assistant to save a document through `add_document`; try cloud links of the other services.
- `list_inbox` / `resolve_inbox` over MCP.
- ODT/ODS, RTF, archives.
- Resolve names read from documents against persons more cleverly (SPEC 9); namesakes.
- `get_file` over MCP; export / import of a person (SPEC 2a).

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
- No linter beyond `go vet` / `gofmt`; `tools/ocrbench` has no tests (throwaway harness).
- Router provider `codex-oauth` disabled; GPT models via codex subscription not benchmarked.
- `РАЗБИВКА.md` (hand split of the sample, outside the repo) needs no separate review: disputed splits are settled in «Входящие».
