# librarian

A pedantic librarian as a service. Each account keeps the documents of one or more
people. Documents arrive whenever they arrive — files, folders, links to shared cloud
folders — and the librarian fingerprints, reads, splits and catalogues every one of
them (rules first, AI models through
[model-router-mcp](https://github.com/wildcar/model-router-mcp) where they measurably
win). Users work with their archive through an MCP server, from any AI assistant.
Accounts are isolated from each other.

Status: running at `https://lib.wildcar.ru` (web interface and MCP at `/mcp`); see `AGENTS/SPEC.md`.

## Run locally

```
go build ./...
export LIBRARIAN_HOME=~/librarian-data          # data root, one folder per account
go run ./cmd/librarian account create family
go run ./cmd/librarian ingest --account family ~/Documents/scans
go run ./cmd/librarian dupes  --account family
go run ./cmd/librarian stats  --account family

# page text: PDF text layer or OCR through model-router-mcp; needs poppler
export ROUTER_URL=https://<router>/mcp/ ROUTER_TOKEN=<token>
go run ./cmd/librarian extract --account family --dry-run   # pages and cost estimate
go run ./cmd/librarian extract --account family
go run ./cmd/librarian text    --account family <sha256 prefix>
go run ./cmd/librarian verify  --account family   # tesseract second reading for older pages
go run ./cmd/librarian split   --account family   # cut originals into documents
go run ./cmd/librarian docs    --account family
go run ./cmd/librarian digest  --account family   # essential text + de-identified summary
go run ./cmd/librarian date    --account family   # creation dates
go run ./cmd/librarian show    --account family <document id> [--layer essential]
go run ./cmd/librarian run     --account family   # every stage above in order, for new files

# persons, index and the MCP server for assistants
go run ./cmd/librarian person add --account family --code ИПС --label Папа --name "Иванов Петр Сергеевич" --alias "Иванов П.С." --birth 1950-02-01
go run ./cmd/librarian index  --account family
go run ./cmd/librarian token create --account family    # shown once
go run ./cmd/librarian serve  --addr 127.0.0.1:8090     # MCP at http://127.0.0.1:8090/mcp, Authorization: Bearer <token>
```

`serve` also serves the web interface on every other path and, with `ROUTER_URL` and
`ROUTER_TOKEN` set, processes new files in the background (uploads, server folders,
cloud links, files an assistant sends with `add_document`). Web login is by a letter:
`librarian account email <account> <e-mail>` binds an address; settings in `deploy/README.md`.

## Docs

- `AGENTS.md` — entrypoint for AI agents working in this repo (workflow, rules, doc map).
- `AGENTS/SPEC.md` — functional specification.
- `docs/adr/` — architecture decisions.
- `deploy/` — deployment on the server; the service runs at `https://lib.wildcar.ru/mcp`.
