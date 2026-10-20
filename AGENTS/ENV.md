# Environment

Host facts, tools, credentials, and command cheat-sheet for this project.
Update whenever a new tool, credential, or host-specific quirk is learned.

## Host

- **Dev and prod**: one Ubuntu 24.04 server `homesrv` (x86_64, 4 cores, 7.6 GB RAM, no GPU — surya stays off), since 2026-10-20. Repo at `/home/wildcar/repo/librarian`.
- Public name of the service: `lib.wildcar.ru` (DNS already points here via `wildcar.ru`). Apache owns ports 80/443 with Let's Encrypt certificates; other MCP services on this host (rtorrent-mcp, yt-dlp-mcp) run as systemd units.
- model-router-mcp runs on another server: `https://mr.wildcar.org/mcp/`.
- Former dev: Windows 10, repo at `C:\Users\sergey_e\D\repo\librarian`; its quirks are kept below for `tools/ocrbench`.
- Archive for experiments on Windows: `C:\Users\sergey_e\D\personal\library\` (only `_эксперименты` reports with personal data, outside the repo).
- Sample source (read only, Nextcloud-synced): `<Nextcloud>/document/Сережа/Папа/Сканы заключений/`; Nextcloud (`nc.wildcar.ru`) runs on this same server.

## Tools

### Server (dev and prod)

- Go 1.27.3 from go.dev in `/usr/local/go`, linked into `/usr/local/bin` (apt has only 1.22; module floor `go 1.26.0`). gcc from `build-essential`, so `go test -race` works.
- poppler-utils 24.02 (`pdftotext`, `pdftoppm` on PATH; `LIBRARIAN_POPPLER_DIR` not needed).
- tesseract 5.3.4 (apt); language data `rus`, `eng` from tessdata_best and `osd` in `/usr/local/share/tessdata` — set `LIBRARIAN_TESSDATA` to it (apt tessdata has no `rus`).
- Service user `librarian` (system, nologin); data root `/var/lib/librarian`; dev data root `~/librarian-dev`.

### Former dev (Windows)

- Go 1.27 (module floor `go 1.26.0`, raised by modernc.org/sqlite). No C compiler on dev, so `go test -race` is unavailable here.
- poppler 25.07 (winget `oschwartz10612.Poppler`), not on PATH: `%LOCALAPPDATA%/Microsoft/WinGet/Packages/oschwartz10612.Poppler_Microsoft.Winget.Source_8wekyb3d8bbwe/poppler-25.07.0/Library/bin` — set `LIBRARIAN_POPPLER_DIR` to it for `extract` and for the extract tests (they skip without it).
- git 2.53. `gh` CLI is not installed on dev.
- `pdftotext` is available from Git for Windows (`/mingw64/bin`). Not installed: exiftool, qpdf, ImageMagick, pdfinfo.
- tesseract 5.4 (winget `UB-Mannheim.TesseractOCR`) at `%LOCALAPPDATA%/Programs/Tesseract-OCR/tesseract.exe`; language data (`rus` from tessdata_best, `eng`, `osd`) in `%LOCALAPPDATA%/librarian/tessdata`.
- llama.cpp (winget `ggml.llamacpp`): `llama-server.exe` under `%LOCALAPPDATA%/Microsoft/WinGet/Packages/ggml.llamacpp_*`, used by surya.
- Python: system 3.14; bench venvs `tools/ocrbench/.venv` (3.14) and `.venv312` (3.12 via uv, for surya/paddle, which lack 3.14 wheels).
- OCR bench cache (rendered pages, engine outputs): `%LOCALAPPDATA%/librarian/ocrbench`.
- Service data root on dev: `%LOCALAPPDATA%/librarian/home` (account `sergey`, holds the sample folder plus the bench PDF copies as a duplicate demo).
- model-router-mcp: MCP over Streamable HTTP at `<router>/mcp`, Bearer token. Repo: https://github.com/wildcar/model-router-mcp

## Credentials & secrets

- Dev: `ROUTER_URL`, `ROUTER_TOKEN` and paths in the repo's `.env` (gitignored, mode 600).
- Service: `/etc/librarian/librarian.env` (root:librarian 640, systemd EnvironmentFile, no `$VAR` expansion); edit with `sudoedit`.
- Server and dev use separate router tokens. Pointers only, never values here.

## Commands cheat-sheet

### Server

```
set -a; . ./.env; set +a      # LIBRARIAN_HOME, ROUTER_URL, ROUTER_TOKEN, tesseract paths
go build ./... && go vet ./... && gofmt -l . && go test ./... && go test -race ./...
go build -o bin/librarian ./cmd/librarian
bin/librarian ingest --account sergey <folder>
bin/librarian extract --account sergey [--dry-run] --workers 6
```

### Former dev (Windows)

```
go build ./...
go vet ./...
gofmt -l .
go test ./...
go run ./cmd/librarian version
go build -o $LOCALAPPDATA/librarian/bin/librarian.exe ./cmd/librarian
LIBRARIAN_HOME=$LOCALAPPDATA/librarian/home librarian ingest --account sergey <folder>
set -a; . ./.env; set +a      # ROUTER_URL, ROUTER_TOKEN
LIBRARIAN_POPPLER_DIR=<poppler bin> librarian extract --account sergey [--dry-run] --workers 6
LIBRARIAN_TESSERACT=$LOCALAPPDATA/Programs/Tesseract-OCR/tesseract.exe LIBRARIAN_TESSDATA=$LOCALAPPDATA/librarian/tessdata librarian verify --account sergey --workers 6
```

## Host-specific quirks

### Server

- Ubuntu's tesseract is built with OpenMP: parallel processes starve each other (5 pages: over 3 min instead of 24 s). `pages.Tesseract.Read` sets `OMP_THREAD_LIMIT=1`; do the same when running tesseract by hand in parallel.
- The service user reads Nextcloud data only through ACLs: traverse (`--x`) on the parent folders and read-only `rX` (plus default ACL) on each source folder. Do not add `librarian` to `www-data`: that group can write all Nextcloud files.
- Run the CLI as the service: `sudo -u librarian sh -c 'cd /var/lib/librarian; set -a; . /etc/librarian/librarian.env; set +a; exec /usr/local/bin/librarian "$@"' librarian <args>`; install with `go build -o bin/librarian ./cmd/librarian && sudo install -m 755 bin/librarian /usr/local/bin/`.

### Former dev (Windows)

- uv here fails to create the Python minor-version link (`Missing expected target directory`), but the venv still works; set `UV_PYTHON_INSTALL_DIR=%LOCALAPPDATA%/librarian/uv-python`.
- torch and paddle in one process: import torch first, otherwise `shm.dll` fails to load.
- PaddleOCR 3.x on Windows CPU: `enable_mkldnn=False` and `PP-OCRv5_mobile_det` (the server detector crashes with an access violation); set the recogniser explicitly (`eslav_PP-OCRv5_mobile_rec`), otherwise it silently picks one without Cyrillic.
- surya 0.22 under CPU contention returns empty pages without an error; run it alone.
- MCP Python SDK (new API): `streamable_http_client(url, http_client=create_mcp_http_client(headers=...))`; result fields are `structured_content` / `is_error`.
- Git Bash mangles Cyrillic in Python stdin heredocs; write the script to a file and run `python -X utf8`.
- Editing Go/Python sources through a Python script written by a shell heredoc turns `\n` inside string literals into real newlines; write edit scripts with the file tool, or edit the source directly.
