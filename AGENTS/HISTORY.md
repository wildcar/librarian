# History

Newest first. Each entry ≤5 lines using the format defined in `AGENTS.md`.

---

## 2026-10-21 · Web interface: login by letter and the document tree
- What: ADR-0007 (web + e-mail login for people and assistants, OAuth for MCP next); `service.sqlite` registry (e-mails, one-time links, sessions); letters over SMTP to the local Postfix; web login (button on the link page, the asking browser polls); tree with chosen levels and the document page; `/mcp` routed explicitly; `account email|unbind|list`, `login-link`.
- Why: owner wants uploads and an overview in a browser and one e-mail login for the web and assistants.
- Files: docs/adr/0007-web-interface-and-email-login.md, internal/users/*, internal/mail/mail.go, internal/web/*, internal/catalog/shelf.go, internal/catalog/dates.go, internal/mcpserver/server.go, cmd/librarian/main.go, deploy/librarian.service
- Fixes-on-the-fly: the registry was created 0644 (chmod 0600, unit `UMask=0077`); a link outlived its e-mail's unbinding.; `Referrer-Policy: no-referrer` made browsers post the login button with `Origin: null`, refused as a foreign site (now `same-origin`).
- Next: SPF/DKIM/DMARC and PTR for wildcar.ru; OAuth for MCP; background pipeline and uploads.

## 2026-10-21 · Refinement by an assistant over MCP
- What: MCP `update_document` (persons, characteristics, date; reason required) marked `assistant`, below the owner and above batch/rule; review until `doc confirm` / `doc revert`; every refinement logged in `doc_changes`; `index.Doc` reindexes one document.
- Why: owner wants corrections said to the assistant ("this is mother's") to reach the library, visibly marked.
- Files: internal/catalog/manual.go, internal/catalog/catalog.go, internal/catalog/persons.go, internal/catalog/batches.go, internal/index/index.go, internal/mcpserver/*, internal/report/report.go, cmd/librarian/main.go, docs/adr/0006-batches-and-characteristics.md
- Fixes-on-the-fly: a jsonschema description starting with «ключ=…» made AddTool panic for every account; reworded, the test caught it.
- Next: characteristics read by a model; `add_document` uploads as batches.

## 2026-10-21 · Manual refinement of one document
- What: `librarian doc set` (person link/unlink, characteristics add/rule out, date full/partial/none/clear) and `doc show` with provenance; index never re-derives what the owner ruled out; MCP date source «уточнена владельцем».
- Why: owner: documents get refined later (date, person, outdated facts) and the refinement must hold.
- Files: internal/catalog/manual.go, internal/catalog/catalog.go, internal/catalog/persons.go, internal/index/*, internal/report/report.go, internal/mcpserver/server.go, cmd/librarian/main.go, docs/adr/0006-batches-and-characteristics.md, AGENTS/SPEC.md
- Next: refinements over MCP; characteristics read by a model.

## 2026-10-21 · Batches: a person and characteristics for everything that came in together
- What: ADR-0006; catalog batches (migration turns intake runs into batches), `ingest --person/--name/--attr/--batch-label`, `batch list|set`; index inherits batch persons and characteristics (manual > batch > rule, conflict → review); MCP attrs, batches, `attrs`/`batch` filters, `missing` for any key.
- Why: 8 of 68 sample documents could not be linked from their text; the owner wants to say whose a batch is and give it characteristics instead of smarter name rules.
- Files: docs/adr/0006-batches-and-characteristics.md, internal/catalog/batches.go, internal/catalog/catalog.go, internal/catalog/persons.go, internal/index/*, internal/intake/intake.go, internal/mcpserver/*, internal/report/report.go, cmd/librarian/main.go
- Next: manual per-document links and characteristics; `add_document` uploads as batches; one pipeline command.

## 2026-10-21 · Date next to a signature is the document's date
- What: dating prompt rule (pipeline 3) and the year restored from the quote for a --MM-DD answer (pipeline 4); sample 64 of 68 dated by quote.
- Why: owner looked at two consultation forms left undated: the handwritten date beside the signature is the conclusion date.
- Files: internal/dating/dating.go, internal/dating/dating_test.go, AGENTS/SPEC.md, AGENTS/MEMORY.md
- Next: open characteristics and tags; one command for the whole pipeline.

## 2026-10-21 · Document date is the creation date, confirmed by a quote
- What: `librarian date` (internal/dating): a model names the creation date, a quote and its basis; the quote must hold the date; partial dates kept; index uses it, the summary only as a fallback; MCP returns date_partial, date_source, a review reason for rejected dates.
- Why: a referral or report with two dates in the summary's first sentence stayed undated; owner: the creation date is the main date.
- Files: internal/dating/*, internal/catalog/dates.go, internal/catalog/catalog.go, internal/catalog/persons.go, internal/index/index.go, internal/mcpserver/server.go, internal/report/report.go, cmd/librarian/main.go, AGENTS/SPEC.md
- Next: open characteristics and tags; one command for the whole pipeline.

## 2026-10-21 · Search pages through the whole archive; review reasons
- What: search_documents returns total and next_offset, lists everything without words, searches by absent date/person (`missing`); `review` lists kinds of open checks in search and get_document.
- Why: an assistant's feedback could not tell whether it had seen the whole archive (59 of 60) or what needs_review meant.
- Files: internal/catalog/persons.go, internal/mcpserver/server.go, internal/mcpserver/server_test.go, AGENTS/SPEC.md, AGENTS/MEMORY.md
- Fixes-on-the-fly: get_document `checks` echoed the leak check's hit (a caught name); reasons now name the kind only.
- Next: document date as creation date with no undated dated documents (replace DateFromSummary); open characteristics and tags.

## 2026-10-21 · Service deployed at lib.wildcar.ru
- What: systemd unit, Apache HTTPS proxy with Let's Encrypt and a reload hook, account token; copies in deploy/.
- Why: assistants connect from anywhere (STATE Next).
- Files: deploy/*, AGENTS/ENV.md, AGENTS/STATE.md
- Fixes-on-the-fly: ProxyPreserveHost Off — the MCP SDK refused the public Host header.
- Next: connect a real assistant; surname+year person matching; one-shot pipeline command.

## 2026-10-21 · Sample run on the server; tesseract thread fix
- What: whole pipeline on the sample account from Nextcloud ($1.68 total, 68 documents, 60 linked); tesseract runs with OMP_THREAD_LIMIT=1; read-only ACLs for the service user.
- Why: first run hung for an hour — OpenMP threads of parallel tesseract processes starved 4 cores.
- Files: internal/pages/tesseract.go, AGENTS/ENV.md, AGENTS/STATE.md
- Next: systemd unit and Apache vhost for lib.wildcar.ru; surname+year person matching.

## 2026-10-20 · Ubuntu server as dev and prod host
- What: installed Go 1.27.3, poppler, tesseract with tessdata_best rus; build, vet, gofmt, tests and -race pass; service user, data dir and env files for router tokens.
- Why: owner moved dev to the server and chose it for the service at lib.wildcar.ru.
- Files: AGENTS/ENV.md, AGENTS/STATE.md
- Fixes-on-the-fly: dropped stale STATE items (tesseract check already shipped, -race now runs, "where it runs" resolved: no GPU).
- Next: owner fills tokens; sample run on the server; systemd unit and Apache vhost.

## 2026-10-20 · Fifth Go slice: persons, index, tokens, MCP server
- What: catalog migration 7 (persons, doc_persons, doc dates, docs_fts), internal/index, account tokens, internal/mcpserver (stateless, per-account server), CLI person/index/token/serve.
- Why: ADR-0004 front door; verified with a real MCP client and an isolation test with two accounts.
- Files: internal/catalog/persons.go, internal/index/*, internal/account/token*.go, internal/mcpserver/*, internal/layers/layers.go, cmd/librarian/main.go, docs/adr/0004-*.md
- Next: one-shot pipeline command, add_document, deployment behind HTTPS.

## 2026-10-20 · Fourth Go slice: essential text and de-identified summaries
- What: internal/layers (rules, line-number removal, repeat and date/medical safeguards, leak and number checks, retry, flags), catalog migration 6 (texts), CLI digest/show.
- Why: ADR-0005 in the service; sample: 69 documents, no leaks, 1 justified flag, $0.045.
- Files: internal/layers/*, internal/catalog/texts.go, internal/catalog/catalog.go, internal/report/report.go, cmd/librarian/main.go, docs/adr/0005-*.md
- Next: MCP server and persons registry.

## 2026-10-20 · Third Go slice: splitting into documents
- What: internal/split (rules port, LCS ratio, whole-file model call with windows, review on strong disagreement), catalog migration 5 (documents, split), CLI split/docs, calibration test.
- Why: ADR-0003 in the service; sample: 100% vs hand labels after making two unreliable rules weak.
- Files: internal/split/*, internal/catalog/documents.go, internal/catalog/catalog.go, internal/report/report.go, cmd/librarian/main.go, docs/adr/0003-*.md
- Next: essential text and summaries (ADR-0005) in Go.

## 2026-10-20 · Tesseract second reading
- What: internal/pages/tesseract.go (TSV with confidences), internal/extract/check.go (share + missing-run rule), migrations 3-4, `verify` command, flagged pages in `stats`; ADR-0002 section on the check.
- Why: ADR-0002 decision 2; calibrated on 156 pages: 0 false flags, one Argon typo correction found.
- Files: internal/pages/*, internal/extract/*, internal/catalog/*, internal/report/report.go, cmd/librarian/main.go, docs/adr/0002-ocr-engine.md
- Next: splitting into documents (ADR-0003) in Go.

## 2026-10-19 · Second Go slice: verbatim page text
- What: internal/pages (poppler, image prep), internal/router (MCP client), internal/extract; catalog migration 2 (pages, extraction); CLI extract/text; tests with a fake router.
- Why: OCR stage of ADR-0002 in the service; run on the sample: 156 pages, $1.39, 98% facts.
- Files: internal/pages/*, internal/router/*, internal/extract/*, internal/catalog/*, cmd/librarian/main.go, internal/report/report.go
- Next: splitting into documents (ADR-0003) in Go.

## 2026-10-19 · First Go slice: accounts, catalog, intake, duplicates
- What: internal/account, internal/catalog (SQLite, migrations), internal/intake (walk, sha256, MIME, unchanged detection, missing flags), internal/report; CLI account/ingest/dupes/stats; tests.
- Why: owner's go-ahead for the first service slice after the measured choices.
- Files: cmd/librarian/main.go, internal/*, go.mod, AGENTS.md, AGENTS/*
- Next: OCR stage in Go through the router.

## 2026-10-19 · GPT-6 Luna for text layers
- What: ADR-0005 records GPT-6 Luna for essential text and summaries, and why the layers stay out of the OCR call.
- Why: owner's choice after the layers experiment.
- Files: docs/adr/0005-text-layers-and-deidentified-summaries.md, AGENTS/SPEC.md, AGENTS/STATE.md, AGENTS/MEMORY.md
- Next: first Go slice of the service.

## 2026-10-19 · Text layers experiment
- What: tools/ocrbench/layers.py (line-number removal, de-identified summary, leak/number/loss checks, header safeguard); 4 models on 68 documents; ADR-0005 measurement section.
- Why: measure ADR-0005 before building it: which model, cost, leaks, lost facts.
- Files: tools/ocrbench/layers.py, tools/ocrbench/README.md, docs/adr/0005-text-layers-and-deidentified-summaries.md, AGENTS/*
- Next: owner picks the model; then the first Go slice of the service.

## 2026-10-19 · ADR-0004 questions resolved; ADR-0005 text layers
- What: owner's answers recorded (MCP upload now, token auth, per-account spend, export/import instead of shared persons, originals not cached); ADR-0005: verbatim, essential, de-identified summary.
- Why: the owner wants least data sent to the assistant and no noise in analysis.
- Files: docs/adr/0004-*.md, docs/adr/0005-text-layers-and-deidentified-summaries.md, docs/filing.md, AGENTS.md, AGENTS/*
- Next: experiment with essential text and summaries on the 68 sample documents.

## 2026-10-19 · Service shape (ADR-0004); splitting rechecked without handwritten dates
- What: ADR-0004 accepted (accounts, persons, stream intake, MCP output); SPEC, filing.md, AGENTS.md rules reworked; split.py --strip-hand-date, ADR-0003 rechecked (Argon still 100%).
- Why: owner described the service shape; handwritten dates were his marks for one batch only.
- Files: docs/adr/0004-service-accounts-persons-mcp.md, docs/adr/0003-*.md, docs/filing.md, AGENTS.md, AGENTS/*, tools/ocrbench/split.py
- Next: answer ADR-0004 open questions; first Go slice with account storage and intake.

## 2026-10-19 · ADR-0003 accepted
- What: owner accepted splitting by one whole-file Argon call with text rules as a cross-check.
- Why: best result in the splitting experiment; rules stay as an explainable check and fallback.
- Files: docs/adr/0003-splitting-scans-into-documents.md, AGENTS/SPEC.md, AGENTS/STATE.md, AGENTS/MEMORY.md
- Next: first Go pipeline slice (folder → hashes → catalog), people registry draft.

## 2026-10-19 · Splitting experiment
- What: tools/ocrbench/split.py (rules, decide, hybrid, whole-file chat); hand labels for 124 boundaries; ADR-0003 proposed; filing.md splitting section rewritten.
- Why: scans hold several documents; needed a measured way to cut them.
- Files: tools/ocrbench/split.py, docs/adr/0003-splitting-scans-into-documents.md, docs/filing.md, AGENTS/*
- Next: owner reviews the proposed split; then the first Go pipeline slice.

## 2026-10-19 · Gemini 4 Argon is the primary OCR model
- What: ADR-0002 updated: primary reader `google/gemini-4-argon` pinned by model_id; round-2 data added; alternatives revised.
- Why: owner chose quality on handwriting (94% vs 74%) over 7× lower cost.
- Files: docs/adr/0002-ocr-engine.md, AGENTS/SPEC.md, AGENTS/STATE.md, AGENTS/MEMORY.md
- Next: splitting experiment on the sample scans.

## 2026-10-19 · OCR round 2: 12 newer OpenRouter models
- What: owner adopted 12 models into the router registry; ran them on the 14 eval pages. Gemini 4 Argon 99% (handwriting 94%) at $0.0097/page.
- Why: owner asked to look for better OCR models on OpenRouter.
- Files: tools/ocrbench/run_router.py (--pause), AGENTS/STATE.md, AGENTS/MEMORY.md; report in library/_эксперименты/ocr-2026-10/
- Next: owner decides whether Gemini 4 Argon replaces Gemini 3.1 Flash Lite as the primary reader (ADR-0002).

## 2026-10-18 · ADR-0002 accepted
- What: owner allowed sending all scanned pages, medical included, to vision models through the router; ADR-0002 → Accepted.
- Why: the privacy question was the only thing holding the OCR decision.
- Files: docs/adr/0002-ocr-engine.md, AGENTS/SPEC.md, AGENTS/STATE.md, AGENTS/MEMORY.md
- Next: splitting experiment on the sample scans.

## 2026-10-18 · OCR bake-off
- What: tools/ocrbench (render, tesseract, paddle, surya, router models, fact scorer); ADR-0002 proposed: router vision model + tesseract cross-check.
- Why: all 156 sample pages are image-only; OCR quality decides everything downstream.
- Files: tools/ocrbench/*, docs/adr/0002-ocr-engine.md, AGENTS/SPEC.md, AGENTS/ENV.md, AGENTS/MEMORY.md
- Next: owner's privacy decision; splitting experiment on the cached tesseract text.

## 2026-10-18 · Filing design
- What: docs/filing.md (layout, naming, sidecar, splitting, collections, people); SPEC requirements; ADR-0001 accepted.
- Why: user's answers on cloud sources, naming and access; analysed 32 sample scans (all image-only).
- Files: docs/filing.md, AGENTS/SPEC.md, AGENTS/MEMORY.md, AGENTS/ENV.md, docs/adr/0001-go-as-core-language.md
- Next: OCR bake-off on the samples → ADR-0002.

## 2026-10-18 · Repository initialised
- What: filled the agent-docs template, draft SPEC, ADR-0001 (Go, Proposed), Go module + CLI stub.
- Why: start of the librarian project.
- Files: AGENTS.md, AGENTS/*, docs/adr/0001-go-as-core-language.md, go.mod, cmd/librarian/main.go
- Next: specify «подшивка», first slice — local folder → hashes → SQLite catalog.
