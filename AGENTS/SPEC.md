# librarian — functional & technical specification

Source of truth for *what the product does* and *how it is built*. Keep it in sync
with reality (update before code when the contract changes).

Status: **draft**. The filing model («подшивка») is not specified yet — see
Open questions in `AGENTS/STATE.md`.

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

## Stack

- Go (see `docs/adr/0001-go-as-core-language.md`, Proposed).
- MCP client to model-router-mcp: `github.com/modelcontextprotocol/go-sdk`.
- Catalog: SQLite (`modernc.org/sqlite`, pure Go).
- External extractors: poppler, tesseract, exiftool, LibreOffice headless (planned).

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

1. ⏳ Accept local files and folders as input.
2. ⏳ Accept links to files/folders in cloud storage (providers: open question).
3. ⏳ Fingerprint and deduplicate files by content.
4. ⏳ Extract text and metadata algorithmically; OCR when there is no text layer.
5. ⏳ Classify documents: rules first, models through the router as fallback.
6. ⏳ File documents into the archive structure («подшивка») — to be specified.
7. ⏳ Several people (a family): owners, shared and personal documents, per-person model cost.

## Project structure

```
cmd/librarian/   CLI entrypoint
internal/        application packages (added as the pipeline appears)
docs/adr/        architecture decisions
AGENTS/          agent docs: SPEC, STATE, HISTORY, MEMORY, ENV
```

## Deployment

- Not decided. Today: a local CLI binary (`go build ./cmd/librarian`).

## Current state

- ✅ Repository skeleton, Go module, CLI stub.
- ⏳ Everything in Functional requirements.

See `AGENTS/STATE.md` for the live Now / Next snapshot.

## Data sources & dependencies

- model-router-mcp: `/mcp` over Streamable HTTP, Bearer auth; every call carries
  `external_user_id` (the family member) and app identity.
- Cloud storage providers: to be chosen.
