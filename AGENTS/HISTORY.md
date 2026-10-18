# History

Newest first. Each entry ≤5 lines using the format defined in `AGENTS.md`.

---

## 2026-10-18 · Filing design
- What: docs/filing.md (layout, naming, sidecar, splitting, collections, people); SPEC requirements; ADR-0001 accepted.
- Why: user's answers on cloud sources, naming and access; analysed 32 sample scans (all image-only).
- Files: docs/filing.md, AGENTS/SPEC.md, AGENTS/MEMORY.md, AGENTS/ENV.md, docs/adr/0001-go-as-core-language.md
- Next: OCR bake-off on the samples → ADR-0002.

## 2026-10-18 · Repository initialised
- What: filled the agent-docs template, draft SPEC, ADR-0001 (Go, Proposed), Go module + CLI stub.
- Why: start of the librarian project.
- Files: AGENTS.md, AGENTS/*, docs/adr/0001-go-as-core-language.md, go.mod, cmd/librarian/main.go
- Next: specify «подшивка», first slice — local folder → hashes → SQLite catalog.
