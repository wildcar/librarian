# State

Current snapshot. Overwrite this file each iteration. Aim for ≤50 lines: keep pointers and
the live picture here; push detail into `AGENTS/SPEC.md` (the contract) and `AGENTS/HISTORY.md`
(the log).

## Goal

Order a family document archive: ingest local and cloud files, analyse them with rules
and models (via model-router-mcp), file them into a shared structure.

## Now

- Repository initialised: draft SPEC, ADR-0001 (Go, Proposed), Go module with a CLI stub.

## Next

- Confirm the language choice (ADR-0001 → Accepted).
- Specify «подшивка»: what the target archive structure looks like.
- First pipeline slice: walk a local folder → sha256 + MIME → SQLite catalog → dedup report.

## Open questions

- «Подшивка»: folder taxonomy, virtual collections over a catalog, or both? Who decides the taxonomy?
- Cloud providers: Yandex Disk, Google Drive, OneDrive, S3, others? Read-only or write back?
- Where does the archive live: local disk, NAS, cloud? Where does the librarian run?
- Family model: accounts, shared vs personal documents, access rules.
- Interface: CLI only, or a web UI / Telegram bot later?

## Deferred

- Tests and linter beyond `go vet` / `gofmt`: no code to test yet.
