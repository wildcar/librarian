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

1. ✅ Accounts: create, isolate storage (`internal/account`); per-account Bearer token (`librarian token create`). E-mail login (ADR-0007): `librarian account create --email E <id>` / `account email <id> <e-mail>` / `unbind` / `list` bind e-mails in `<home>/service.sqlite` (0600); only the administrator binds, an unknown address gets the same page and no letter. The web logs in by a letter: a one-time link (15 min, 5 letters an hour per address) opens a page whose button grants the login; the browser that asked polls and gets its session too (30 days, sliding); `librarian login-link <e-mail>` prints a link for when mail fails. ⏳ Then: OAuth 2.1 for MCP with the same letter (the assistant's name in it); export or delete an account as one directory.
2. ✅ Persons within an account (`librarian person add|list`): code, label, full name, aliases, birth date. `librarian index` links documents by full name, alias or birth date; manual links are never overwritten.
2b. ✅ Batches (ADR-0006): what came in at one time. `librarian ingest --person CODE [--name … --label … --birth … --alias …] --attr KEY=VALUE … --batch-label L` labels the batch — an existing person or a new one, and open characteristics (several values per key); `librarian batch list | set` (`--person`, `--no-person`, `--attr`, `--unset KEY[=VALUE]`) changes it later and reindexes. A run makes a batch of the originals new to the account; a labelled run takes every original it saw; a run with nothing new and no labels makes none. `index` gives each document its batches' persons (`method = batch`) and characteristics (`source = batch:<id>`). Precedence manual > batch > rule; a rule finding a person other than the batch's keeps both links and adds a `review` reason. ⏳ Then: batches for uploads and links.
2c. ✅ Manual refinement of one document (`librarian doc set`, `doc show`): `--person` links, `--unlink` / `--no-person` rule a person out, `--attr` adds, `--unset KEY[=VALUE]` rules a value or a whole key out, `--date` (full or partial), `--no-date`, `--clear-date`. Manual facts outrank batches, rules and models; ruled-out persons and values are never derived again by `index`; a manual date replaces the derived one (provenance `manual`, MCP «уточнена владельцем») and silences a rejected-date review. `doc show` prints every fact with its source and the change log. `--reason` goes to the log.
2d. ✅ Refinement by an assistant (MCP `update_document`: add/remove persons and characteristics, date or `none`, `clear_date`; `reason` with the user's words is required). Its facts are marked `assistant`, rank below the owner's and above batches, rules and models, and keep a `review` reason until the owner runs `librarian doc confirm` (they become the owner's) or `doc revert` (they are dropped). An assistant cannot change what the owner set or ruled out by hand; such a change is refused whole. Every refinement, the owner's too, is logged (`doc_changes`).
2a. ⏳ Export a person's documents from one account and import them into another; the copies are independent afterwards. Persons are never shared between accounts.

Intake

3. ✅ Folder intake (`librarian ingest`): skips OS/sync/temporary files and counts them, re-reads only files whose size or mtime changed, flags vanished sources as missing without deleting them. ⏳ Then: files sent through the MCP tool `add_document` (kept, they have no other source); e-mail and Telegram intake later.
4. ⏳ Accept shared-folder links, read-only: Yandex Disk, Google Drive, Nextcloud.
5. ✅ Fingerprint (sha256, MIME by content with extension as a refinement for containers, mismatch reported) and duplicate report (`librarian dupes`) within the account. Catalog keeps one original, many `source_uri`. Originals of folder and link sources are not kept: `source_uri` + sha256, fetched on demand and checked; uploaded files are kept.

Analysis

6. ✅ Verbatim page text (`librarian extract`): the PDF text layer where it has at least 100 letters, otherwise the page rendered by `pdftoppm` (grayscale JPEG, long side 2000) and read by `google/gemini-4-argon` through the router; images (JPEG, PNG, TIFF, BMP, GIF, WebP) are scaled the same way. The source is re-hashed before reading; a changed file is an error, not a silent re-read. Pages are stored one by one, so a failed page is retried alone. Every OCR page also gets the tesseract second reading at 300 dpi; a printed page whose confident tesseract words are under 90% present in the Argon text, or that misses 3 consecutive ones, is flagged for the owner (`librarian verify` backfills, `stats` lists flagged pages). ⏳ Then: other types (DOCX, plain text); flagged pages into the inbox. Original contract: a vision model via the router plus tesseract, disagreement on print flags the page (ADR-0002).
7. ✅ Split originals into documents (`librarian split`, `librarian docs`): one-page originals directly; others by one `chat` call with `google/gemini-4-argon` over the page text (windows of 30 pages, seams by rules); strong rules that contradict the model send the file to review. Documents get stable ids `<sha256[:16]>-<first page>` (ADR-0003).
7a. ⏳ Reassemble documents whose pages are not contiguous and restore page order.
8. ✅ Document date = creation date: when the document was written, issued or signed, not the date of what it refers to (`librarian date`, `internal/dating`, after `digest`, before `index`). One model call (GPT-6 Luna) over the essential text returns the date, a quote and the basis; a date at the end of a record or next to a signature is the creation date even when the form's date field is empty; a year the model dropped is restored from the quote when it holds exactly one matching full date (`stated` — named as the document's date; `event` — the date of what the document records). The quote must occur in the text and hold exactly that date, otherwise the answer is rejected and goes to `review`. A date without a day or a year is kept partial (`YYYY-MM`, `YYYY`, `--MM-DD`), never completed by guessing; date filters treat it as no date. `index` uses the confirmed date (provenance `model:<basis>`); no creation date means no date; until the stage has run, or after a rejection, the summary's first sentence stands in (provenance `summary`). ⏳ Then: sanity checks (device clocks, dates before birth).
9. ⏳ Resolve persons named in a document against the account's persons (aliases, birth date); names read from handwriting are never stored as read.
10. ⏳ Classify documents: rules first, models through the router as fallback.
11. ⏳ Anything below confidence thresholds goes to the account's inbox for the owner's decision.
11a. ✅ Essential text (`librarian digest`, `internal/layers`): the verbatim text minus insignificant parts (form notes, ads, contacts, repeated letterheads, barcodes, empty fields), by removal only, checked against the verbatim text (ADR-0005).
11b. ✅ De-identified summary of every document (`librarian digest`; one regeneration on a failed check, then flagged; `librarian show`): no names of patient, doctor or clinic, no identifying numbers; checked by rules (ADR-0005).

Output

12a. ✅ Web interface (ADR-0007) inside `librarian serve`: `/mcp` and `/health` as before, every other path the web (`LIBRARIAN_PUBLIC_URL`; letters over `SMTP_ADDR`, `MAIL_FROM`, optional `SMTP_USER`/`SMTP_PASSWORD`, to the log without `SMTP_ADDR`). «Документы» is a virtual tree whose levels the owner picks (person, year, any characteristic key; «Человек › Год» by default); a document with several values sits under each, one without a value in the level's «нет …» branch. The document page shows date and its source, persons, characteristics, batches, open checks, summary, essential and verbatim text. Forms from another origin are refused; cookies HttpOnly, SameSite=Lax, Secure over HTTPS. ⏳ Then: uploads (files, a server folder within allowed roots, cloud links) with the background pipeline; connected assistants page; the original file.
12. ✅ MCP server (`librarian serve`, ADR-0004): `list_persons`, `search_documents`, `get_document`, `update_document` (2d). `search_documents` without words lists every document, newest first, undated last; it returns `total` and `next_offset` for paging (`offset`, `limit` ≤ 50). `missing` names characteristics that may be absent (`date`, `person`): alone it finds documents lacking them, next to a filter on the same characteristic it widens the filter to them. Documents carry `attrs` (key → values) and `batches`; `search_documents` filters by `attrs` (["key=value"], all must hold) and `batch`, and `missing` takes any characteristic key besides `date` and `person`. Documents carry `date`, or `date_partial` when the day or year is unknown, and `get_document` says where the date came from. Every document carries `review`: the kinds of open checks (page reading, split, creation date, summary), never the text a check caught. ⏳ Then: `list_persons`, `search_documents`, `get_document` (summary by default, essential text on request), `get_file` (original, for verification), `add_document`, `list_inbox` / `resolve_inbox`, `export_person` / `import_person` (draft names, ADR-0004, ADR-0005).
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
- Today: a CLI (`librarian account create | email | unbind | list | login-link | ingest | dupes | stats | extract | verify | split | docs | digest | show | person | index | token | serve | text`); data root from `--home` or `LIBRARIAN_HOME`; `extract` needs `ROUTER_URL`, `ROUTER_TOKEN` and poppler (`LIBRARIAN_POPPLER_DIR` when it is not on PATH).

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
