# History

Newest first. Each entry ≤5 lines using the format defined in `AGENTS.md`.

---

## 2026-10-23 · Russian in old Word files
- What: `.doc` text that comes out of catdoc as cp1252 mojibake (no Cyrillic, mostly À–ÿ) is read again with `-s cp1251`; catdoc's «fast-saved» note is dropped; migration 22 re-reads old .doc files and remakes their layers and dates.
- Why: owner's Word 95 file showed «Êàôåäðà ÝÂÌ» in the essential text on the document page.
- Files: internal/pages/{legacy,office_test}.go, internal/catalog/catalog.go
- Next: owner checks the document page; characteristics read by a model.

## 2026-10-22 · Router calls name lib.wildcar.ru
- What: `chat` calls carry `app_url` from `LIBRARIAN_PUBLIC_URL`; every CLI and worker connection goes through `dialRouter`.
- Why: the router showed librarian's requests under https://mr.wildcar.org, its own address, because no `app_url` was sent.
- Files: internal/router/{router,app_test}.go, cmd/librarian/main.go
- Next: owner checks the router journal.

## 2026-10-22 · Word and Excel files are read
- What: .docx and .xlsx read in Go (tables as «cell | cell», sheets by name, dates from number formats), .doc by catdoc, .xls by python3-xlrd; stored as text pages like plain text, no model, no cost; migration 21 retries files set aside as unsupported.
- Why: owner keeps documents in Word and Excel; they were listed as «не читаются».
- Files: internal/pages/{office,legacy}.go, internal/extract/extract.go, internal/catalog/catalog.go, internal/web/templates/upload.html
- Fixes-on-the-fly: xls2csv shifted dates by four years on a test file, so .xls goes through xlrd.
- Next: try a real .doc; owner's checks of cloud links.

## 2026-10-22 · Public cloud links
- What: `internal/cloud` lists and downloads Yandex Disk, Nextcloud and Google Drive shares (files, folders, Docs as PDF); «Загрузить» takes a link with the same labels; the worker downloads it into the account's store (`pipeline.IngestLink`, batch `kind = link`); «Пачки» shows the queue and download progress.
- Why: ADR-0007 intake — documents kept in clouds, without downloading them by hand.
- Files: internal/cloud/, internal/pipeline/{link,folder,worker}.go, internal/web/{upload,link_test}.go, internal/web/templates/{upload,batches,doc}.html
- Next: try real links of each service; DOCX/XLSX.

## 2026-10-22 · Summary, characteristics and a new person from the page
- What: «Резюме в порядке» / «Сделать заново» (catalog migration 20: `texts.summary_confirmed`, `texts.redo`; the worker remakes the layers, the old ones serve meanwhile); characteristics added and removed on the document page; a new person from the person form, linked here and by name across the account.
- Why: owner wants every inbox reason closed from the web.
- Files: internal/web/{edit,inbox,web}.go, internal/web/templates/{doc,parts}.html, internal/catalog/{texts,persons,catalog}.go
- Next: DOCX/XLSX or cloud links, owner to choose.

## 2026-10-22 · The way back keeps its place
- What: the tree remembers open branches and scroll per tab (`sessionStorage`, branch path by label), the inbox its scroll; «←» on the document page leads to the list it was opened from («Документы» with the same levels, or «Входящие» with its filter).
- Why: owner found «← Документы» always led to a collapsed tree, even from the inbox.
- Files: internal/web/templates/{parts,tree,inbox,doc}.html
- Next: a flagged summary and characteristics from the page.

## 2026-10-22 · Persons and date edited on the document page
- What: «изменить» under «Дата» and «Чей» on the document page: ticked persons become the owner's links, the rest are ruled out (none ticked = nobody's, the inbox stops asking); the date in Russian forms, «нет даты», «вернуть дату библиотеки»; inbox person and date reasons link to the open form.
- Why: owner wants to settle the remaining inbox reasons without the CLI or an assistant.
- Files: internal/web/{edit,inbox,web}.go, internal/web/templates/{doc,parts}.html
- Next: a flagged summary and characteristics from the page.

## 2026-10-22 · Checks settled from the inbox
- What: buttons in «Входящие» and on the document page confirm a disputed split (whole file) or a doubted page, accept or revert an assistant's refinements; catalog migration 19 (`split.confirmed`, `pages.confirmed`, cleared by a new split or reading); «Открыть файл» moved next to each file name.
- Why: owner wants to close checks in one place instead of the CLI; the file button belongs to the file it opens.
- Files: internal/web/{inbox,original,web}.go, internal/web/templates/{doc,inbox,parts}.html, internal/catalog/{persons,pages,documents,catalog}.go
- Next: person, date and summary fixes from the inbox.

## 2026-10-22 · The original on the document page; the inbox
- What: «Открыть файл» serves the original at `/doc/<id>/file` (PDF opens at the document's page; served only while the file hashes to the original; unsafe types only download); «Входящие» (`/inbox`) lists documents without a person and every `review` reason, with counts and a filter by kind.
- Why: owner wants to check a document against its source in one click and see everything waiting for a check on one page instead of per document and in `stats`.
- Files: internal/web/{original,inbox}.go, internal/web/templates/{doc,inbox,parts}.html, internal/catalog/pages.go
- Next: act on inbox items in place (confirm split, page, assistant edit).

## 2026-10-22 · Kind, specialty and organisation read from the text
- What: `titling` became `describe`: one call per document returns the title and `вид`, `специальность`, `организация` (catalog migration 15, `texts.model_attrs`, every document described again); `index` gives them as `source = model` below manual, assistant and batch; the tree can group by them; lists hide model chips, the document page marks them.
- Why: owner wants to find and group documents by kind and specialty, not only by person and year.
- Files: internal/describe/, internal/catalog/{titles,persons,shelf,batches}.go, internal/index/index.go, internal/web/, internal/mcpserver/server.go
- Then: the summary rarely names the author, so the specialty was missing or guessed from referrals; `describe` also reads the essential text (migration 16 describes again) with the summary's leak checks on the title and values.
- Then: owner asked for the specialty to be guessed from the content when not written, marked as a guess (migration 17, `model:guess`, «догадка», MCP `guessed`).
- Next: the original file on the document page; the inbox.

## 2026-10-22 · Short document titles; shorter login texts
- What: a `title` stage after the summary names each document in 2–6 words from the summary (catalog migration 14, `texts.title`, reset when the summary changes); the tree, the document page and MCP use it; login and «Проверьте почту» texts shortened; a year-less date showed as «16.--09».
- Why: owner found list captions long and uneven («Это …», dates in three formats, «оформленные в частной клинике»).
- Files: internal/titling/, internal/catalog/{titles,texts,shelf,persons}.go, internal/pipeline/pipeline.go, internal/web/, internal/mcpserver/server.go
- Next: DOCX/XLSX; archives.

## 2026-10-22 · Plain text files are read; uploads keep their name
- What: `text/plain` originals (.txt, .md, .csv) become pages as they are (UTF-8 or Windows-1251, ~3500-character pages at paragraph breaks); catalog migration 13 puts unsupported text files back to extraction and adds `sources.name`; the document page lists its files, «Пачки» names the files not read.
- Why: owner's notes (ChatGPT answers as .md) were left out of search; uploaded files were known only by hash.
- Files: internal/pages/text.go, internal/extract/extract.go, internal/catalog/{catalog,pages,batches}.go, internal/intake/store.go, internal/web/templates/{doc,batches}.html
- Next: DOCX/XLSX; archives.

## 2026-10-22 · Second reading at a fixed size; clearer batch progress
- What: the tesseract check renders the long side at 3508 px instead of 300 dpi; «Пачки» shows a percent over reading, splitting and search, «готово / в работе / в очереди», and the kinds of files not read.
- Why: scans whose PDF page claims ~60×85 cm became 70-MP images, ten minutes per page, and the run looked hung; a ✓ beside an empty bar confused the owner.
- Files: internal/pages/poppler.go, internal/extract/extract.go, internal/catalog/batches.go, internal/web/templates/batches.html
- Next: read plain text and Markdown files; keep the uploaded file's name.

## 2026-10-22 · A server folder as a source in the web
- What: allowed roots per account (`account root add|remove|list`, `Account.Within` resolves links); «Папка» browses them and queues a folder with labels; the worker runs the folder intake (`pipeline.IngestFolder`, now also behind `ingest`) before processing; «Пачки» shows the queue and the last intake.
- Why: owner wants to add documents already on the server from the browser, without leaving it for the rest of the server.
- Files: internal/account/roots.go, internal/pipeline/folder.go, internal/pipeline/worker.go, internal/web/folder.go, internal/web/templates/*, cmd/librarian/main.go
- Next: cloud links; the original on the document page.

## 2026-10-22 · Uploads through the web, processed in the background
- What: `internal/pipeline` runs extract → split → digest → date → index under a per-account flock (`librarian run`); a worker in `serve` runs it after uploads and hourly, holding runs whose OCR estimate is over `LIBRARIAN_AUTO_LIMIT_USD` until «Обработать»; «Загрузить» stores files in the account and makes a labelled batch; «Пачки» shows progress.
- Why: owner wants to add documents from the browser; the stages were started by hand one by one.
- Files: internal/pipeline/*, internal/intake/store.go, internal/catalog/batches.go, internal/web/upload.go, internal/web/templates/*, cmd/librarian/main.go
- Checked live on a throwaway account: upload → search in 8 s, $0.0002; the account was removed.
- Next: a server folder within allowed roots; cloud links; the original file on the document page.

## 2026-10-22 · Assistants connect by letter (OAuth for MCP)
- What: the service is its own OAuth 2.1 server (metadata, dynamic registration, authorize by the letter or a web session's button, token with PKCE and rotating refresh tokens); `/mcp` behind the SDK's `RequireBearerToken` takes OAuth and account tokens; «Ассистенты» page lists and disconnects; refinements log the assistant (`doc_changes.actor`, catalog migration 12).
- Why: owner wants assistants connected by e-mail like the web, with the assistant named in the letter.
- Files: internal/users/oauth.go, internal/users/users.go, internal/web/oauth.go, internal/web/web.go, internal/web/templates/*, internal/mcpserver/server.go, internal/catalog/catalog.go, internal/catalog/manual.go, internal/account/token.go, internal/report/report.go, cmd/librarian/main.go
- Next: connect Claude for real; background pipeline and uploads.

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

