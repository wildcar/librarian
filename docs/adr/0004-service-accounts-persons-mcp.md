# ADR-0004: A multi-account service — persons inside accounts, an MCP server as the output

**Status:** Accepted (2026-10-19; open questions resolved the same day)
**Date:** 2026-10-19
**Deciders:** wildcar
**Scope:** whole product: data model, storage layout, interfaces, deployment

---

## Context

The first sketch was a local tool that orders one family's archive. The owner has
settled on a different shape:

- The service has users, and each user is isolated from every other user.
- One user may keep the documents of several people (the owner keeps his own and his
  father's).
- Documents arrive as an irregular stream, not as one big import.
- The service catalogues them and exposes them to the user through an MCP server, so
  the user works with the archive from any AI assistant they like.

## Decision

**Terms.**

- *Account* — a user of the service and the unit of isolation. Everything (documents,
  persons, catalog, files, model spend) belongs to exactly one account.
- *Person* — someone whose documents the account keeps: a code, full name, aliases,
  birth date. An account has one or more persons. Persons of different accounts are
  unrelated even when they are the same human being.
- *Document* — one logical paper document (after splitting, ADR-0003), owned by one
  account and attached to one or more of its persons.

**Isolation.** Each account has its own storage root: its own SQLite catalog and its own
file tree (originals cache, PDF cuts, sidecars). No query, tool or job can address two
accounts. Physical separation is chosen over a shared database with an account column,
so that an isolation bug cannot leak data and an account can be exported or deleted as
one directory.

**Input.** An asynchronous pipeline per account: intake → fingerprint/dedup → OCR
(ADR-0002) → split (ADR-0003) → extract facts (dates, persons, type) → file. Intake
accepts single files, folders and shared-folder links (Yandex Disk, Google Drive,
Nextcloud), and files sent through the MCP tool `add_document`. E-mail and a Telegram
bot are later channels. Each item is processed when it arrives; nothing waits for a
batch.

**Output.** One MCP endpoint (Streamable HTTP). Connecting an assistant needs exactly
two things: the endpoint address and the account's token (`Authorization: Bearer`).
The token identifies the account; every tool runs inside that account only. OAuth may
come later. What the tools return by default is set by ADR-0005 (text layers). First
tools (names are a draft):

| Tool | Purpose |
|---|---|
| `list_persons` | persons of the account |
| `search_documents` | full-text and facet search: person, type, date range, institution |
| `get_document` | metadata and the de-identified summary; the essential text on request (ADR-0005) |
| `get_file` | the original, fetched from its source for verification |
| `add_document` | put a file or a link into the intake |
| `list_inbox` / `resolve_inbox` | items waiting for the owner's decision |
| `export_person` / `import_person` | hand one person's documents to another account |

**Dates** come from the document content (rules first, model second). The handwritten
dates of the first sample batch are not a signal the service may rely on.

**Deployment.** A long-running service, reachable from the internet over HTTPS; the
Ubuntu server on the owner's LAN is the first candidate.

## Consequences

- `docs/filing.md` applies per account: one archive tree per account, persons as its
  top level. The reserved per-document `visibility` field is superseded by account
  isolation.
- The local file tree is no longer the only interface; the MCP server is the main one.
  The tree remains as an export and as the source the catalog can be rebuilt from.
- The service needs account management and per-account credentials, which a local tool
  did not.
- Model spend is attributed per account: every router call carries the account as
  `external_user_id` (`librarian:<account_id>`), so the router console and `get_usage`
  show each account separately while the prefix still groups the whole service.
- Go fits the shape (ADR-0001): one long-running binary, the official MCP Go SDK for
  the server side as well as the router client.

## Resolved questions (owner, 2026-10-19)

- **Intake channels:** files, folders, links, and an MCP upload tool from the start;
  e-mail and Telegram later.
- **Authentication:** as simple as possible — the MCP address plus a per-account token.
  OAuth probably later.
- **Router attribution:** per account.
- **Persons shared between accounts:** no, too fragile. Instead one account can export a
  person's documents (say, a child's) and another account imports them; after that the
  two copies live independently.
- **Originals of link sources:** not cached. An original is only needed to check a fact
  against the primary source; the service keeps its `source_uri` and sha256 and fetches
  it on demand, flagging a mismatch. Analysis and answers work from text (ADR-0005).
  A file that arrives through `add_document` has no other source, so the service keeps
  that one.

## Alternatives considered

- **A local single-user tool** (the first sketch) — no isolation or remote access; does
  not fit an AI assistant as the front end.
- **One shared database with an account column** — simpler to operate, but one missing
  `WHERE account_id` leaks another family's medical records.
- **A web UI as the main interface** — more to build, and the owner prefers talking to
  the archive through an assistant; a UI may come later for the inbox.
