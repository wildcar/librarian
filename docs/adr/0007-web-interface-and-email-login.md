# ADR-0007: Web interface and e-mail login for people and assistants

**Status:** Accepted (2026-10-21)
**Date:** 2026-10-21
**Deciders:** wildcar
**Scope:** service-level user registry, `librarian serve` (web pages, OAuth endpoints, background pipeline), mail sending, Apache vhost `lib.wildcar.ru`, ADR-0004 (authentication, intake channels)

---

## Context

Today the only way in is the MCP endpoint with a per-account Bearer token, created by
`librarian token create` and pasted into the assistant by hand. Documents enter only
through `librarian ingest` over a server folder, and the pipeline (extract → split →
digest → date → index) is started by hand, command by command.

The owner wants (2026-10-21):

- a web interface next to MCP: upload files (a server folder, files dropped into the
  page, a cloud link) and browse a virtual tree of documents;
- login by e-mail, the account named by its e-mail, the same scheme for the web and for
  assistants: an assistant without a token gives the account's e-mail, the service
  mails a link, the owner clicks it, the assistant is in; the letter says which
  assistant asked.

ADR-0004 put a web UI aside (the owner talks to the archive through an assistant) and
OAuth «later». Both arrive now: the web for intake and overview, the assistant stays the
way to ask questions.

Constraints found on the host:

- MCP clients (claude.ai, ChatGPT, Claude Code) connect only through the MCP
  authorization spec: OAuth 2.1 with PKCE, dynamic client registration, discovery by
  `/.well-known` metadata. Without a token they get 401 and open the authorization page
  in a browser; they never call a tool before that, so the e-mail cannot be passed as a
  tool argument.
- The Go MCP SDK (v1.8.0) has the resource-server side (`auth.RequireBearerToken`,
  protected resource metadata) and the metadata types; the authorization endpoints are
  ours to write.
- Postfix runs on the host (`myhostname = mail.wildcar.ru`) and accepts mail from
  localhost; outbound port 25 is open and mail to mail.ru has been delivered. But
  `wildcar.ru` has no SPF, DKIM, DMARC or MX record, and the address 95.165.109.250 has
  no reverse DNS (PTR): Gmail rejects mail from addresses without PTR, others tend to
  file such mail as spam.

## Decision

### Users and accounts

A service-level registry `<home>/service.sqlite`, separate from account catalogs:

```
users    (email PRIMARY KEY, account, created)      email lower-cased; account = directory id
logins   (hash, email, purpose, client_id, created, expires, used, state)
sessions (hash, email, created, expires, last_seen) web sessions
clients  (client_id, secret_hash, name, redirect_uris, created)  dynamic registrations
codes    (hash, request, email, expires, used, grant_id)          5 minutes, one use
grants   (id, client_id, email, host, created, last_used, revoked) one per connected assistant
tokens   (hash, grant_id, kind, expires, used)                    kind: access | refresh
```

The account directory keeps its short id (`sergey`); the e-mail is the login and is
bound to the account by the administrator: `librarian account create <id> --email …` or
`librarian account email <id> <e-mail>` (`account unbind`, `account list`). Nobody creates an account by entering an
e-mail: a stranger's e-mail gets the same «letter sent» page and no letter (no account
enumeration, no OCR spend by strangers). The table allows several e-mails per account
for a family later.

Only hashes of secrets are stored (links, sessions, tokens), as with today's tokens.

### The login letter

One mechanism for both doors:

1. The person enters the e-mail (web login page, or the authorization page an
   assistant opened).
2. The service mails a one-time link, valid 15 minutes. The letter says what is asked:
   «вход в веб-интерфейс» or «ассистент «Claude» (claude.ai) просит доступ к
   библиотеке», plus the time and the browser/IP of the request.
3. The link opens a page with a button «Разрешить» / «Войти». A plain GET grants
   nothing: mail scanners open links in letters by themselves.
4. The page that waits in the original browser notices the confirmation (polling) and
   continues there: a web session cookie, or the redirect back to the assistant with
   an authorization code. So the letter may be opened on a phone while the assistant
   runs on a computer.

Rate limits per e-mail and per IP keep the form from being used to flood a mailbox.

### MCP: OAuth 2.1, the service as its own authorization server

- `/.well-known/oauth-protected-resource` and `/.well-known/oauth-authorization-server`
  metadata; 401 on `/mcp` carries `WWW-Authenticate` with the metadata address.
- `/oauth/register` — dynamic client registration; `client_name` and the redirect
  host are what the letter shows. The name is self-declared, so the letter shows the
  redirect host next to it (`claude.ai`), which the client cannot fake.
- `/oauth/authorize` — the e-mail form and the letter above; PKCE S256 required. A
  browser that already has a web session gets one «Разрешить» button instead of a letter:
  that session came from a letter. An unregistered redirect gets an error page, never a
  redirect.
- `/oauth/token` — code exchange and refresh. Access tokens live 1 hour, refresh tokens
  90 days and rotate on use. A code or refresh token used twice revokes the connection.
  Public clients (PKCE only) and confidential ones (a secret) are both accepted; loopback
  redirects match on any port (RFC 8252).
- The web page «Ассистенты» lists the connected clients (name, host, last use) and
  revokes them.
- The client's name goes into the refinement log: `update_document` records «ассистент
  Claude» rather than just «assistant».

Static tokens (`librarian token create`) stay for scripts and for clients without
OAuth; they are accepted on `/mcp` next to OAuth tokens.

### Web interface

Inside `librarian serve`, on the same address: `/mcp` and `/oauth/*` as above,
everything else the web. Server-rendered Go templates with a little JavaScript,
embedded in the binary: no build step, no separate frontend. Pages:

- **Документы** — the virtual tree: levels built from the catalog, not from folders.
  The owner picks the levels from person, year and any characteristic key («Человек ›
  Год» by default, «Тема › Человек» and so on, owner's choice 2026-10-21); a document
  with several values on a level appears under each, and documents without one sit in a
  «нет …» branch of that level. A document page
  shows the summary, the essential text, the date with its source, persons,
  characteristics, batches and open checks; the owner sees the original.
- **Загрузить** — three sources, each making a batch (ADR-0006) with persons and
  characteristics given in the same form as `ingest`:
  - a server folder, chosen only inside the roots the administrator allowed for the
    account (`librarian account root add|remove|list <id> [path]`, kept in the account's
    `roots` file); a path is resolved with its symbolic links before the check, so
    neither `..` nor a link leads out; the web never browses the rest of the server. The
    worker takes the folder like `ingest` (same batch rules, shared code) and the files
    stay where they are;
  - files dropped into the page (size limit per file and per request), kept in the
    account's store, since they have no other source (ADR-0004);
  - a public cloud link: Yandex Disk and Nextcloud shares (files and folders), a Google
    Drive file; the downloaded originals are kept too, because links die.
- **Пачки** — batches, their labels, processing progress and cost.
- **Ассистенты** — connected assistants and static tokens.

### Processing in the background

`serve` runs a worker that takes new originals of every account through the whole
pipeline (extract → split → digest → date → index), one account at a time, after an
upload and every hour, and shows progress per batch. When the OCR estimate of what waits
exceeds `LIBRARIAN_AUTO_LIMIT_USD` ($1 by default), the account waits for the owner's
«Обработать» in the web; the approval covers that run only. The same runner is
`librarian run --account A` for the CLI. A lock file (`flock`) per account keeps a CLI
run and the worker from doing the same work twice.

### Mail

The service hands letters to the local Postfix (`SMTP_ADDR=127.0.0.1:25`, no login,
`MAIL_FROM=library@wildcar.ru`; settings in `librarian.env`, so an outside relay can
replace it later without code changes). `wildcar.ru` becomes a proper sender (owner's
choice, 2026-10-21):

- SPF `v=spf1 ip4:95.165.109.250 -all`, DKIM signing by OpenDKIM in Postfix, DMARC
  `p=none` with reports at first, MX `mail.wildcar.ru`;
- PTR `95.165.109.250 → mail.wildcar.ru`, set by the provider of the address — without
  it Gmail refuses the letter whatever SPF and DKIM say.

When a letter does not arrive, the administrator prints a one-time link
(`librarian login-link <e-mail>`): whoever opens it and presses the button logs in, so
a lost letter never locks the owner out.

### Order of work

1. Registry, mail, web login by letter, an empty web with the account's documents as a
   list.
2. The virtual tree and the document page.
3. OAuth for MCP and the «Ассистенты» page.
4. Background pipeline (`librarian run`), then uploads: files, server folder, cloud
   links.

## Open questions

- Whether the provider sets the PTR record; if not, an outside relay with a login.
- Several people per account (a family, each with their own e-mail): allowed by the
  table, not designed further yet.

## Consequences

- An assistant is connected by entering the address and the owner's e-mail; no token
  is copied by hand. Each assistant has its own revocable access, and its edits are
  signed with its name.
- The service gains an authorization server: more code to keep correct (PKCE, code
  reuse, redirect URI matching, token rotation). It stays small because there is one
  identity provider (the letter) and no third-party login.
- Access depends on mail delivery; static tokens and the CLI remain as the way in when
  mail fails.
- Uploads make the service store originals for the first time and spend model money
  without the administrator at the console; the cost limit per batch and the
  admin-only accounts bound that.
- The web reads only the account of the session's e-mail, through the same per-account
  catalog as MCP; isolation (ADR-0004) does not change.

## Alternatives considered

- **A login tool inside MCP** (the assistant calls `login(email)` and gets a token) —
  clients do not call tools before they are authorized and do not store tokens a tool
  returns; it would also put the token into the chat.
- **Third-party login (Google, Yandex ID)** — no letters to deliver, but ties the
  library to an outside account and the owner asked for e-mail.
- **Codes typed by hand instead of links** — works when mail is opened on another
  device, but the waiting page already covers that, and a link is one tap.
- **A separate frontend (SPA)** — a build toolchain and an API layer for a handful of
  pages; server-rendered templates are enough.
- **Open registration by e-mail** — anyone could create an account and spend OCR money
  on the owner's router key.
