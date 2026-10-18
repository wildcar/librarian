# Environment

Host facts, tools, credentials, and command cheat-sheet for this project.
Update whenever a new tool, credential, or host-specific quirk is learned.

## Host

- **Dev**: Windows 10, PowerShell 7 + Git Bash, repo at `C:\Users\sergey_e\D\repo\librarian`.
- **Prod**: not decided. Candidate: Ubuntu server on the LAN, reachable from the internet.
- Archive for experiments: `C:\Users\sergey_e\D\personal\library\` (empty as of 2026-10-18).
- Sample source (read only, Nextcloud-synced, can be slow): `C:\Users\sergey_e\D\personal\nextcloud\document\Сережа\Папа\Сканы заключений\`.

## Tools

- Go 1.27 (module declares `go 1.24` as the floor).
- git 2.53. `gh` CLI is not installed on dev.
- `pdftotext` is available from Git for Windows (`/mingw64/bin`). Not installed: exiftool, qpdf, ImageMagick, pdfinfo.
- tesseract 5.4 (winget `UB-Mannheim.TesseractOCR`) at `%LOCALAPPDATA%/Programs/Tesseract-OCR/tesseract.exe`; language data (`rus` from tessdata_best, `eng`, `osd`) in `%LOCALAPPDATA%/librarian/tessdata`.
- llama.cpp (winget `ggml.llamacpp`): `llama-server.exe` under `%LOCALAPPDATA%/Microsoft/WinGet/Packages/ggml.llamacpp_*`, used by surya.
- Python: system 3.14; bench venvs `tools/ocrbench/.venv` (3.14) and `.venv312` (3.12 via uv, for surya/paddle, which lack 3.14 wheels).
- OCR bench cache (rendered pages, engine outputs): `%LOCALAPPDATA%/librarian/ocrbench`.
- model-router-mcp: MCP over Streamable HTTP at `<router>/mcp`, Bearer token. Repo: https://github.com/wildcar/model-router-mcp

## Credentials & secrets

- `ROUTER_URL` and `ROUTER_TOKEN` live in the repo's `.env` (gitignored). Pointers only, never values here.

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

- uv here fails to create the Python minor-version link (`Missing expected target directory`), but the venv still works; set `UV_PYTHON_INSTALL_DIR=%LOCALAPPDATA%/librarian/uv-python`.
- torch and paddle in one process: import torch first, otherwise `shm.dll` fails to load.
- PaddleOCR 3.x on Windows CPU: `enable_mkldnn=False` and `PP-OCRv5_mobile_det` (the server detector crashes with an access violation); set the recogniser explicitly (`eslav_PP-OCRv5_mobile_rec`), otherwise it silently picks one without Cyrillic.
- surya 0.22 under CPU contention returns empty pages without an error; run it alone.
- MCP Python SDK (new API): `streamable_http_client(url, http_client=create_mcp_http_client(headers=...))`; result fields are `structured_content` / `is_error`.
- Git Bash mangles Cyrillic in Python stdin heredocs; write the script to a file and run `python -X utf8`.
