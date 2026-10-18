# librarian

A pedantic librarian for a family document archive. Give it local files or folders,
or links to cloud storage; it fingerprints, deduplicates and reads every document,
classifies it (rules first, AI models through
[model-router-mcp](https://github.com/wildcar/model-router-mcp) when rules are not
enough), and files it into an ordered archive shared by several people.

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
