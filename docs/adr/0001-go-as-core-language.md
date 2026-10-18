# ADR-0001: Go as the core language

**Status:** Proposed
**Date:** 2026-10-18
**Deciders:** wildcar
**Scope:** whole repository

---

## Context

The librarian walks large document archives (local folders and cloud storage),
fingerprints and deduplicates files, extracts text and metadata, classifies
documents with rules first and AI models second, and files them into an
ordered archive shared by several people (a family).

Model calls go through `model-router-mcp` (Python, MCP over Streamable HTTP), so
the librarian does not need any ML library in-process: it is an MCP client.

The work splits into two kinds:

- I/O-heavy, long-running orchestration: crawling, hashing, cloud APIs, a job
  queue, a catalog database, a service that runs for days.
- Content extraction: PDF text layer, OCR, Office formats, EXIF.

## Decision

Write the core in Go.

- Official MCP client: `github.com/modelcontextprotocol/go-sdk`.
- Catalog: SQLite through `modernc.org/sqlite` (pure Go, no cgo, builds on
  Windows and Linux alike).
- Content extraction uses best-of-breed external tools behind a narrow
  interface (`pdftotext`/poppler, `tesseract`, `exiftool`, LibreOffice
  headless for Office formats), or the router itself (`image-to-text` for OCR of
  hard scans, `local/uvdoc` for page unwarping). A Go-native extractor replaces
  a tool only where it is clearly as good.

## Consequences

- One static binary, cheap concurrency for crawling and hashing, a stable
  long-running process.
- Go's document-parsing libraries are weaker than Python's; we pay for that with
  external tool dependencies, which must be listed in `AGENTS/ENV.md` and
  checked at startup.
- Two languages across the ecosystem (Go here, Python in the router); the
  boundary is MCP, so they never share code.

## Alternatives considered

- **Python** — the richest document ecosystem (pypdf, unstructured, docling,
  python-docx) and the same language as the router. Rejected for the core
  because the hard part here is reliable long-running orchestration, not
  parsing; can still appear later as an extractor sidecar if a Python-only
  library proves necessary.
- **Rust** — strong performance and safety, slower to develop in, thinner MCP
  and document ecosystem than either Go or Python.
