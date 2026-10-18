# librarian — functional & technical specification

Source of truth for *what the product does* and *how it is built*. Keep it in sync
with reality (update before code when the contract changes).

Status: **draft**. Filing layout and naming: `docs/filing.md`.

## Purpose

A pedantic librarian for a family document archive. It takes local files or
folders, or links to files/folders in cloud storage, analyses every file, and
files it into an ordered archive that several people (family members) share.
Photos may join later.

Analysis combines two kinds of functions:

- **Algorithmic** — anything that can be done well without a neural network:
  hashing, exact and near duplicates, file type by magic bytes, metadata, text
  layer extraction, dates and identifiers by pattern.
- **Model-based** — classification, summaries, OCR of hard scans, entity
  extraction, chosen by a flexible policy and called through
  [model-router-mcp](https://github.com/wildcar/model-router-mcp).

## Principles

1. Originals are never modified or deleted by analysis. Any change to files on
   disk or in the cloud is a plan first (dry run), applied only on confirmation.
2. Every fact about a document carries provenance: which function produced it
   (rule or model id), when, and with what confidence.
3. Algorithmic first, model second: a model is called only when rules cannot
   answer with enough confidence. Model cost is tracked per family member.
4. Processing is idempotent and resumable: a file already analysed (same
   content hash, same pipeline version) is not analysed again.
5. Sources are read-only. The archive (a local tree, possibly cloud-synced by the
   user) holds derived artefacts: a Markdown sidecar per logical document and,
   per source policy, a PDF cut of it. The archive can rebuild the catalog.
6. Extraction quality beats language purity: an extractor is chosen by measured
   results on real samples, whether it is a Go library, an external tool, a
   Python sidecar or a model through the router.

## Stack

- Go (see `docs/adr/0001-go-as-core-language.md`).
- MCP client to model-router-mcp: `github.com/modelcontextprotocol/go-sdk`.
- Catalog: SQLite (`modernc.org/sqlite`, pure Go).
- OCR: vision model through the router (primary) plus local tesseract as a second reading (ADR-0002, Proposed).
- Bake-off harness for extractors: `tools/ocrbench/` (Python).

## Architecture (sketch)

```
sources ──→ ingest ──→ fingerprint ──→ extract ──→ classify ──→ file («подшивка»)
 local fs    enumerate   sha256, size,   text layer,  rules first,   place into the
 cloud       files,      MIME by magic,  metadata,    then model     ordered archive
             watch       dedup           EXIF, OCR    via router     (plan → apply)
                              │              │            │
                              └──────────── catalog (SQLite) ──────── provenance, cost
                                                          │
                                                 model-router-mcp (MCP, Streamable HTTP):
                                                 chat, decide, image-to-text, local/uvdoc
```

## Functional requirements

1. ⏳ Accept local files and folders as input; slow synced folders are read once into a local cache by hash.
2. ⏳ Accept shared-folder links, read-only: Yandex Disk, Google Drive, Nextcloud.
3. ⏳ Fingerprint and deduplicate files by content; one original, many `source_uri`.
4. ⏳ Extract text and metadata algorithmically; OCR when there is no text layer: a vision model via the router plus tesseract, disagreement on print flags the page (ADR-0002).
4a. ⏳ Names and dates read from handwriting are resolved against the people registry and date rules, never stored as read.
5. ⏳ Split multi-document scans into logical documents by page ranges.
6. ⏳ Classify documents: rules first, models through the router as fallback.
7. ⏳ File into the archive tree with standard names `YYYY-MM-DD <PERSON> <Title>.<ext>` (`docs/filing.md`).
8. ⏳ Virtual collections: saved catalog queries materialised as `Коллекции/<name>.md`.
9. ⏳ People registry (code, name, aliases, birth date); document owner and a reserved `visibility` field. Access is not enforced yet.

## Project structure

```
cmd/librarian/   CLI entrypoint
docs/filing.md   archive layout, naming, sidecar format
internal/        application packages (added as the pipeline appears)
docs/adr/        architecture decisions
tools/ocrbench/  OCR bake-off: render pages, run engines, score against key facts
AGENTS/          agent docs: SPEC, STATE, HISTORY, MEMORY, ENV
```

## Deployment

- Not decided: the dev machine or an Ubuntu server on the LAN reachable from the internet.
  Today: a local CLI binary (`go build ./cmd/librarian`).
- Archive: a local folder; the catalog database lives outside it (sync clients and SQLite do not mix).

## Current state

- ✅ Repository skeleton, Go module, CLI stub.
- ⏳ Everything in Functional requirements.

See `AGENTS/STATE.md` for the live Now / Next snapshot.

## Data sources & dependencies

- model-router-mcp: `https://mr.wildcar.org/mcp/` over Streamable HTTP, Bearer auth; every call carries
  `external_user_id="librarian"` (groups the librarian's spend in router stats) and `app_name="librarian"`.
- Cloud sources: Yandex Disk, Google Drive, Nextcloud — shared-folder links, read-only.
