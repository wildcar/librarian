# Memory

Durable agent memory for this repository: working agreements and facts that are NOT
derivable from the code, git history, or SPEC/STATE/HISTORY.

This is the ONLY agent memory store in the project. Do not use external or per-tool memory
stores — memory must travel with the repo (see AGENTS.md -> Memory). Read at the start of
every session; when you learn something durable, append a short bullet here and commit it
together with the related change.

MEMORY.md = durable facts/agreements; current state -> STATE.md; iteration log -> HISTORY.md.

## Working agreements (feedback)

- Extraction/OCR quality comes first; Python utilities are acceptable when Go or external tools fall short (user, 2026-10-18). Why: the archive is useless if the text is wrong.
- Cloud sources are only read, never written; locally the archive keeps text copies and summaries (user, 2026-10-18).
- Access control is not needed now, only a hook for it: one human librarian keeps everyone's documents (user, 2026-10-18).
- Router calls carry `external_user_id="librarian"` (user, 2026-10-18). Why: the librarian's spend shows separately in the router console and `get_usage`; the field does not affect access.

## Project facts

- model-router-mcp (Python, separate repo) is the only way to reach AI models; it logs cost by `external_user_id`, so the librarian passes the family member's id there.
- Sample set: 32 scanned medical PDFs from a Brother scanner, 1–15 pages, no text layer at all; part named `Scan_20190101_…` because the scanner clock was reset. Paths: `AGENTS/ENV.md`.
- OCR bake-off 2026-10-18 (ADR-0002): on print all good engines reach 96–99%; handwriting separates them (Gemini Flash Lite 74%, tesseract 9%). Vision models invent plausible wrong surnames from handwriting; tesseract fails visibly.
- Router provider `codex-oauth` was disabled on 2026-10-18; OpenAI models were reached through OpenRouter instead.
- Router tools useful here: `chat` (text and images), `decide` (typed yes/no, choice, score questions with probabilities), `local/uvdoc` (unwarps photographed pages).
