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
- Router calls carry `external_user_id="librarian:<account_id>"` in the service (user, 2026-10-19; earlier just `librarian`). Why: spend per account in the router console and `get_usage`; the field does not affect access.
- Connecting an assistant must stay simple: MCP address + account token; OAuth later (user, 2026-10-19).
- Persons are never shared between accounts; sharing is export from one account and import into another (user, 2026-10-19). Why: shared ownership is too fragile.
- Originals are only for verification against the primary source; analysis uses the essential text, most requests the de-identified summary without patient, doctor or clinic (user, 2026-10-19, ADR-0005).

## Project facts

- model-router-mcp (Python, separate repo) is the only way to reach AI models; it logs cost by `external_user_id`, so the librarian passes the family member's id there.
- Sample set: 32 scanned medical PDFs from a Brother scanner, 1–15 pages, no text layer at all; part named `Scan_20190101_…` because the scanner clock was reset. Paths: `AGENTS/ENV.md`.
- OCR bake-off 2026-10-18 (ADR-0002): on print all good engines reach 96–99%; handwriting separates them (Gemini Flash Lite 74%, tesseract 9%). Vision models invent plausible wrong surnames from handwriting; tesseract fails visibly.
- Primary OCR model is `google/gemini-4-argon`, pinned by `model_id` (user, 2026-10-19). Why: best on handwriting (94%) at ~$0.01/page; the router tiers it `expensive`, so tier-based selection would never pick it.
- OCR round 2 (2026-10-19): Gemini 4 Argon best (99%, handwriting 94%). Claude Haiku 5.5 scores 96% but invented a malignant diagnosis on a handwritten note — aggregate scores hide dangerous hallucinations. DeepSeek v4.1 Flash loops to the 131k output limit; Mistral Small 4 hits provider 429 even with 20 s pauses.
- New router models stay unconfirmed until their first successful request; a registry prune removes unconfirmed rows (owner, 2026-10-19).
- Handwritten dates on the first page of documents (58 of 68 in the sample) were the owner's own marks for that batch only (user, 2026-10-19). Why it matters: do not use them as a signal; document dates come from the content. Without them, splitting rules drop to 75% recall while the whole-file Argon call stays at 100%.
- Splitting multi-document scans: one whole-file chat call with Gemini 4 Argon, text rules as a cross-check; `decide` is not used for splitting (user, 2026-10-19, ADR-0003). Why: 100% on 27 files against 94% for rules and 78% for decide.
- Essential text and summaries: `openai/gpt-6-luna` (user, 2026-10-19, ADR-0005). The layers are not folded into the OCR call: OCR is per page and precedes splitting, and the verbatim layer must stay a pure reading.
- Text layers (2026-10-19): a model told to drop repeated headers drops every copy, and a running header may be the only place the patient's name is — always restore the first copy. Claude Haiku 5.5 leaked a clinic as an acronym and (earlier) invented a diagnosis; Gemini 3.1 Flash Lite invented a date — neither suits medical text.
- Go pipeline OCR (pdftoppm grayscale, long side 2000, same prompt) matches the Python bench: 98% vs 99% facts, $1.39 for 156 pages (2026-10-19).
- Gemini 4 Argon sometimes corrects typos of the original despite the prompt (seen once in 156 pages, 2026-10-20); the tesseract check surfaces such cases as missing words.
- Second-reading thresholds were set on the sample (min real share 0.92, max missing run 1); revisit them on the first batch from another clinic.
- Without the owner's handwritten dates, "same letterhead" and "same contract number" are wrong as strong split signals (2026-10-20): one clinic's form or contract carries many separate documents. Recalibrate rules with `TestCalibrateRules` on new batches.
- GPT-6 Luna summaries sometimes compute new numbers (two 14-day courses → «28 дней»); the number check flags them, which is intended (2026-10-20).
- Date parsing for checks must handle: abbreviated/upper-case months, Roman months with Cyrillic Х, month-first «ДЕК 4, 2019», no swallowing on rejected candidates, sizes «5 × 9,12», times before dates (tests in internal/layers).
- MCP tokens are `lib_<account>_<secret>`; the secret is base64url and may contain `_`, so split on the first `_` after the prefix (a flaky-test lesson, 2026-10-20).
- FTS5 in modernc sqlite: use the table name, not an alias, in MATCH and bm25().
- Router `decide` replies carry cost in `cost_usd` (not `cost`).
- Router provider `codex-oauth` was disabled on 2026-10-18; OpenAI models were reached through OpenRouter instead.
- Router tools useful here: `chat` (text and images), `decide` (typed yes/no, choice, score questions with probabilities), `local/uvdoc` (unwarps photographed pages).
