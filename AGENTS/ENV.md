# Environment

Host facts, tools, credentials, and command cheat-sheet for this project.
Update whenever a new tool, credential, or host-specific quirk is learned.

## Host

- **Dev**: Windows 10, PowerShell 7 + Git Bash, repo at `C:\Users\sergey_e\D\repo\librarian`.
- **Prod**: not decided.

## Tools

- Go 1.27 (module declares `go 1.24` as the floor).
- git 2.53. `gh` CLI is not installed on dev.
- Planned external extractors (not required yet): poppler (`pdftotext`), tesseract, exiftool, LibreOffice.
- model-router-mcp: MCP over Streamable HTTP at `<router>/mcp`, Bearer token. Repo: https://github.com/wildcar/model-router-mcp

## Credentials & secrets

- Router URL and token will live in `.env` (gitignored). Pointers only, never values here.

## Commands cheat-sheet

### Dev

```
go build ./...
go vet ./...
gofmt -l .
go test ./...
go run ./cmd/librarian version
```

## Host-specific quirks

### Dev

- —
