# Memory

Durable agent memory for this repository: working agreements and facts that are NOT
derivable from the code, git history, or SPEC/STATE/HISTORY.

This is the ONLY agent memory store in the project. Do not use external or per-tool memory
stores — memory must travel with the repo (see AGENTS.md -> Memory). Read at the start of
every session; when you learn something durable, append a short bullet here and commit it
together with the related change.

MEMORY.md = durable facts/agreements; current state -> STATE.md; iteration log -> HISTORY.md.

## Working agreements (feedback)

- —

## Project facts

- model-router-mcp (Python, separate repo) is the only way to reach AI models; it logs cost by `external_user_id`, so the librarian passes the family member's id there.
- Router tools useful here: `chat` (text and images), `decide` (typed yes/no, choice, score questions with probabilities), `local/uvdoc` (unwarps photographed pages).
