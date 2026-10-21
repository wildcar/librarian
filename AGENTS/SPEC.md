# librarian — functional & technical specification

Source of truth for *what the product does* and *how it is built*. Keep it in sync
with reality (update before code when the contract changes).

Status: **draft**. Service shape: `docs/adr/0004-service-accounts-persons-mcp.md`.
Filing layout and naming: `docs/filing.md`.

## Purpose

A pedantic librarian as a service. Each user of the service (an *account*) keeps the
documents of one or more people (*persons*): their own, a parent's, a child's.
Accounts are isolated from each other. Documents arrive as an irregular stream —
files, folders, links to shared cloud folders — and the librarian catalogues every one
of them. The user works with the archive through an MCP server, from any AI assistant
they like. Photos may join later.

Analysis combines two kinds of functions:

- **Algorithmic** — anything that can be done well without a neural network:
  hashing, exact and near duplicates, file type by magic bytes, metadata, text
  layer extraction, dates and identifiers by pattern.
- **Model-based** — OCR, splitting scans into documents, classification, summaries,
  entity extraction, chosen by a flexible policy and called through
  [model-router-mcp](https://github.com/wildcar/model-router-mcp).

## Principles

1. Accounts are isolated: separate storage root, separate catalog, separate files.
   No operation can address two accounts (ADR-0004).
2. Originals are never modified or deleted by analysis. Any change to files on
   disk or in the cloud is a plan first (dry run), applied only on confirmation.
3. Every fact about a document carries provenance: which function produced it
   (rule or model id), when, and with what confidence.
4. Algorithmic first, model second: a model is called only when rules cannot
   answer with enough confidence or measurably lose (ADR-0002, ADR-0003). Model cost
   is tracked per account.
5. Processing is idempotent and resumable: a file already analysed (same
   content hash, same pipeline version) is not analysed again.
6. Sources are read-only. The account's archive holds derived artefacts: a Markdown
   sidecar per logical document and, per source policy, a PDF cut of it. The archive
   can rebuild the catalog.
7. Extraction quality beats language purity: an extractor is chosen by measured
   results on real samples, whether it is a Go library, an external tool, a
   Python sidecar or a model through the router.
8. Nothing relies on marks a particular owner happened to make on paper (such as a
   handwritten date); facts come from the document's own content.
9. Least data by default: MCP tools return de-identified summaries; the essential text,
   the verbatim text and the original only on explicit request (ADR-0005).

## Stack

- Go (see `docs/adr/0001-go-as-core-language.md`), one long-running service.
- MCP: `github.com/modelcontextprotocol/go-sdk` — server for users, client for the router.
- Catalog: SQLite per account (`modernc.org/sqlite`, pure Go).
- OCR: `google/gemini-4-argon` through the router, pinned by `model_id` (primary), plus local tesseract as a second reading (ADR-0002).
- Splitting: one whole-file `chat` call with `google/gemini-4-argon`, text rules as a cross-check (ADR-0003).
- Text layers: original (reference only), verbatim OCR text, essential text, de-identified summary; essential text and summary by `openai/gpt-6-luna` after rules, with checks (ADR-0005).
- Bake-off harness for extractors: `tools/ocrbench/` (Python): OCR, splitting, text layers.

## Architecture (sketch)

```
                       ┌──────────────────── account A (isolated) ────────────────────┐
 files, folders,       │ intake → fingerprint → OCR → split → extract facts → file     │
 cloud share links ───►│ queue    sha256, MIME  (02)   (03)   dates, persons,  tree +  │
 (stream, any time)    │          dedup                       type, summary    sidecars │
                       │                    └──────── catalog.sqlite ────────┘         │
                       └────────────────────────────────▲──────────────────────────────┘
                                                        │ account resolved from credential
 AI assistant (any) ──── MCP (Streamable HTTP, HTTPS) ──┘  list_persons, search_documents,
                                                           get_document, get_file, add_document,
                                                           list_inbox / resolve_inbox
 pipeline ──── MCP client ────► model-router-mcp: chat (OCR, split), decide, local/uvdoc
```

## Functional requirements

Accounts and persons

1. ✅ Accounts: create, isolate storage (`internal/account`); per-account Bearer token (`librarian token create`). ⏳ Then: export or delete an account as one directory. Connecting an assistant needs only the MCP address and the account's Bearer token; OAuth later.
2. ✅ Persons within an account (`librarian person add|list`): code, label, full name, aliases, birth date. `librarian index` links documents by full name, alias or birth date; manual links are never overwritten. ⏳ Then: the owner links what rules could not.
2a. ⏳ Export a person's documents from one account and import them into another; the copies are independent afterwards. Persons are never shared between accounts.

Intake

3. ✅ Folder intake (`librarian ingest`): skips OS/sync/temporary files and counts them, re-reads only files whose size or mtime changed, flags vanished sources as missing without deleting them. ⏳ Then: files sent through the MCP tool `add_document` (kept, they have no other source); e-mail and Telegram intake later.
4. ⏳ Accept shared-folder links, read-only: Yandex Disk, Google Drive, Nextcloud.
5. ✅ Fingerprint (sha256, MIME by content with extension as a refinement for containers, mismatch reported) and duplicate report (`librarian dupes`) within the account. Catalog keeps one original, many `source_uri`. Originals of folder and link sources are not kept: `source_uri` + sha256, fetched on demand and checked; uploaded files are kept.

Analysis

6. ✅ Verbatim page text (`librarian extract`): the PDF text layer where it has at least 100 letters, otherwise the page rendered by `pdftoppm` (grayscale JPEG, long side 2000) and read by `google/gemini-4-argon` through the router; images (JPEG, PNG, TIFF, BMP, GIF, WebP) are scaled the same way. The source is re-hashed before reading; a changed file is an error, not a silent re-read. Pages are stored one by one, so a failed page is retried alone. Every OCR page also gets the tesseract second reading at 300 dpi; a printed page whose confident tesseract words are under 90% present in the Argon text, or that misses 3 consecutive ones, is flagged for the owner (`librarian verify` backfills, `stats` lists flagged pages). ⏳ Then: other types (DOCX, plain text); flagged pages into the inbox. Original contract: a vision model via the router plus tesseract, disagreement on print flags the page (ADR-0002).
7. ✅ Split originals into documents (`librarian split`, `librarian docs`): one-page originals directly; others by one `chat` call with `google/gemini-4-argon` over the page text (windows of 30 pages, seams by rules); strong rules that contradict the model send the file to review. Documents get stable ids `<sha256[:16]>-<first page>` (ADR-0003).
7a. ⏳ Reassemble documents whose pages are not contiguous and restore page order.
8. ✅ Document date (`librarian index`): taken from the first sentence of the summary, which names the document and its date; provenance `summary`. ⏳ Then: sanity checks (device clocks, dates before birth).
9. ⏳ Resolve persons named in a document against the account's persons (aliases, birth date); names read from handwriting are never stored as read.
10. ⏳ Classify documents: rules first, models through the router as fallback.
11. ⏳ Anything below confidence thresholds goes to the account's inbox for the owner's decision.
11a. ✅ Essential text (`librarian digest`, `internal/layers`): the verbatim text minus insignificant parts (form notes, ads, contacts, repeated letterheads, barcodes, empty fields), by removal only, checked against the verbatim text (ADR-0005).
11b. ✅ De-identified summary of every document (`librarian digest`; one regeneration on a failed check, then flagged; `librarian show`): no names of patient, doctor or clinic, no identifying numbers; checked by rules (ADR-0005).

Output

12. ✅ MCP server (`librarian serve`, ADR-0004): `list_persons`, `search_documents`, `get_document`. `search_documents` without words lists every document, newest first, undated last; it returns `total` and `next_offset` for paging (`offset`, `limit` ≤ 50). `missing` names characteristics that may be absent (`date`, `person`): alone it finds documents lacking them, next to a filter on the same characteristic it widens the filter to them. Every document carries `review`: the kinds of open checks (page reading, split, summary), never the text a check caught. ⏳ Then: `list_persons`, `search_documents`, `get_document` (summary by default, essential text on request), `get_file` (original, for verification), `add_document`, `list_inbox` / `resolve_inbox`, `export_person` / `import_person` (draft names, ADR-0004, ADR-0005).
13. ⏳ Archive tree per account with standard names `YYYY-MM-DD <PERSON> <Title>.<ext>` (`docs/filing.md`), usable as an export.
14. ⏳ Virtual collections: saved catalog queries, materialised as `Коллекции/<name>.md` and available through MCP.

## Project structure

```
cmd/librarian/   service and CLI entrypoint
docs/filing.md   archive layout, naming, sidecar format
docs/adr/        architecture decisions
internal/        application packages (added as the pipeline appears)
tools/ocrbench/  OCR bake-off and splitting experiment: render pages, run engines, score against hand labels
AGENTS/          agent docs: SPEC, STATE, HISTORY, MEMORY, ENV
```

## Deployment

- A long-running service reachable from the internet over HTTPS. First candidate: the
  owner's Ubuntu server on the LAN. Development runs on the Windows dev machine.
- Storage: one directory per account (catalog, file cache, archive tree). The catalog
  database is never inside a folder that a sync client rewrites.
- Today: a CLI (`librarian account create | ingest | dupes | stats | extract | verify | split | docs | digest | show | person | index | token | serve | text`); data root from `--home` or `LIBRARIAN_HOME`; `extract` needs `ROUTER_URL`, `ROUTER_TOKEN` and poppler (`LIBRARIAN_POPPLER_DIR` when it is not on PATH).

## Current state

- ✅ Repository skeleton, Go module, CLI stub.
- ✅ Measured choices: OCR engine (ADR-0002), splitting method (ADR-0003), text layer model (ADR-0005).
- ✅ First Go slice: account storage, per-account SQLite catalog, folder intake, duplicate and stats reports.
- ✅ Second Go slice: verbatim page text through the text layer or OCR via the router, stored per page in the account catalog; tesseract second reading with flags.
- ✅ Third Go slice: splitting originals into documents, documents table in the catalog.
- ✅ Fourth Go slice: essential text and de-identified summary per document with checks.
- ✅ Fifth Go slice: persons registry, document dates, full-text index, account tokens, MCP server with three tools.
- ⏳ Everything in Functional requirements.

See `AGENTS/STATE.md` for the live Now / Next snapshot.

## Data sources & dependencies

- model-router-mcp: `https://mr.wildcar.org/mcp/` over Streamable HTTP, Bearer auth; every call carries
  `external_user_id="librarian:<account_id>"` (per-account spend; the bench tools use plain `librarian`) and `app_name="librarian"`.
- Cloud sources: Yandex Disk, Google Drive, Nextcloud — shared-folder links, read-only.
- poppler command-line tools (`pdftotext`, `pdftoppm`): `poppler-utils` on Linux.
- tesseract 5 with the `rus` and `eng` languages: `tesseract-ocr`, `tesseract-ocr-rus` on Linux.
