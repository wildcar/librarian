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
- The product is a multi-account service: an account (user) keeps documents of several persons, accounts are isolated, input is an irregular stream, output is an MCP server for any AI assistant (user, 2026-10-19, ADR-0004). Supersedes the 2026-10-18 note that access control was not needed.
- All scanned pages, medical ones included, may be sent to vision models through the router (user, 2026-10-18). Why: reading quality on handwriting matters more than keeping scans local; recorded in ADR-0002.
- Router calls carry `external_user_id="librarian"` (user, 2026-10-18). Why: the librarian's spend shows separately in the router console and `get_usage`; the field does not affect access.

## Project facts

- model-router-mcp (Python, separate repo) is the only way to reach AI models; it logs cost by `external_user_id`, so the librarian passes the family member's id there.
- Sample set: 32 scanned medical PDFs from a Brother scanner, 1–15 pages, no text layer at all; part named `Scan_20190101_…` because the scanner clock was reset. Paths: `AGENTS/ENV.md`.
- OCR bake-off 2026-10-18 (ADR-0002): on print all good engines reach 96–99%; handwriting separates them (Gemini Flash Lite 74%, tesseract 9%). Vision models invent plausible wrong surnames from handwriting; tesseract fails visibly.
- Primary OCR model is `google/gemini-4-argon`, pinned by `model_id` (user, 2026-10-19). Why: best on handwriting (94%) at ~$0.01/page; the router tiers it `expensive`, so tier-based selection would never pick it.
- OCR round 2 (2026-10-19): Gemini 4 Argon best (99%, handwriting 94%). Claude Haiku 5.5 scores 96% but invented a malignant diagnosis on a handwritten note — aggregate scores hide dangerous hallucinations. DeepSeek v4.1 Flash loops to the 131k output limit; Mistral Small 4 hits provider 429 even with 20 s pauses.
- New router models stay unconfirmed until their first successful request; a registry prune removes unconfirmed rows (owner, 2026-10-19).
- Handwritten dates on the first page of documents (58 of 68 in the sample) were the owner's own marks for that batch only (user, 2026-10-19). Why it matters: do not use them as a signal; document dates come from the content. Without them, splitting rules drop to 75% recall while the whole-file Argon call stays at 100%.
- Splitting multi-document scans: one whole-file chat call with Gemini 4 Argon, text rules as a cross-check; `decide` is not used for splitting (user, 2026-10-19, ADR-0003). Why: 100% on 27 files against 94% for rules and 78% for decide.
- Router `decide` replies carry cost in `cost_usd` (not `cost`).
- Router provider `codex-oauth` was disabled on 2026-10-18; OpenAI models were reached through OpenRouter instead.
- Router tools useful here: `chat` (text and images), `decide` (typed yes/no, choice, score questions with probabilities), `local/uvdoc` (unwarps photographed pages).
