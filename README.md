# librarian

A pedantic librarian as a service. Each account keeps the documents of one or more
people. Documents arrive whenever they arrive — files, folders, links to shared cloud
folders — and the librarian fingerprints, reads, splits and catalogues every one of
them (rules first, AI models through
[model-router-mcp](https://github.com/wildcar/model-router-mcp) where they measurably
win). Users work with their archive through an MCP server, from any AI assistant.
Accounts are isolated from each other.

Status: early draft, see `AGENTS/SPEC.md`.

## Run locally

```
go build ./...
go run ./cmd/librarian version
```

## Docs

- `AGENTS.md` — entrypoint for AI agents working in this repo (workflow, rules, doc map).
- `AGENTS/SPEC.md` — functional specification.
- `docs/adr/` — architecture decisions.
