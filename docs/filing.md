# Filing («подшивка») — archive layout and naming

Draft, 2026-10-18; revised 2026-10-19 for the service shape (ADR-0004). Contract-level
points are mirrored in `AGENTS/SPEC.md`.

Everything below applies **per account**: each account has its own sources, catalog and
archive tree, and nothing is shared between accounts.

## Two layers: sources and the archive

**Sources** are where originals live. The librarian reads them and never changes them.

- Local folders (including folders synced with Nextcloud, Yandex Disk, Google Drive).
- Shared-folder links: Yandex Disk public links, Google Drive shared folders,
  Nextcloud public shares. Read-only.

Every original is recorded once in the catalog by content hash (sha256), together
with every place it was seen (`source_uri`). Moving or renaming an original does not
create a new document.

**The archive** is the account's folder tree (`<account>/library/`) that the librarian
writes. It is an export and the source the catalog can be rebuilt from; the main way to
use the archive is the MCP server. It holds derived artefacts only:

- a **sidecar** `.md` per logical document: metadata, links back to the original, a
  summary («выжимка») and the full text;
- optionally a **PDF copy** of the logical document (pages cut out of the original
  scan), per source policy: `copy: none | split`. Cloud sources default to `none`.

## Tree

```
<account>/library/
  ЕБВ/                                   one folder per person of the account, named by person code
    Медицина/
      Приемы-назначения/
        2026-07-14 ЕБВ Прием кардиолога.md
        2026-07-14 ЕБВ Прием кардиолога.pdf
      Анализы-исследования/
    Финансы/
    Документы/
  Семья/                                 documents that belong to the household, not one person
  Коллекции/                             virtual collections (see below)
  Входящие/                              not filed yet: low confidence, needs a human decision
```

The domain level (`Медицина`, `Финансы`, ...) and the leaves under it come from a
taxonomy file that a human edits; the librarian only proposes additions.

## File names

`YYYY-MM-DD <PERSON> <Title>.<ext>` — e.g. `2026-07-14 ЕБВ Прием кардиолога.pdf`.

- Date is the **document date** (the visit, the test, the contract), taken from the
  document's content: never the scan or file date, and never a mark someone wrote on
  the paper afterwards. Scanner timestamps are unreliable: part of the sample set is
  named `Scan_20190101_…`, the scanner's clock was reset.
- Partial date: `2026-07` or `2026`. Unknown date: the document goes to `Входящие/`.
- `PERSON` is the person code from the account's persons (initials, `ЕБВ`). A household
  document uses `Семья`.
- Title is short, Russian, sentence case: document type plus the one detail that tells
  it apart (`Прием кардиолога`, `Общий анализ крови`, `Выписка Сбербанк`).
- Forbidden on Windows and stripped: `< > : " / \ | ? *`, trailing dots and spaces.
  Collision: ` (2)`, ` (3)`.
- The sidecar and the PDF copy share the base name.

## Sidecar format

```markdown
---
id: 01JB...                      # stable document id (ULID)
person: ЕБВ
date: 2026-07-14
type: Прием-назначение
title: Прием кардиолога
domain: Медицина
sources:
  - sha256: 9f2c...
    uri: file:///.../Scan_20261016_223616_001925.pdf
    pages: 1-2
facts:                           # every fact keeps its provenance
  date:   {value: 2026-07-14, by: rule/date-pattern, conf: 0.98}
  type:   {value: Прием-назначение, by: model/<model_id>, conf: 0.91}
pipeline: 1                      # pipeline version that produced this file
---

## Выжимка
...

## Текст
...
```

The archive must survive without the catalog: the catalog can be rebuilt by reading
the sidecars. That is also why the catalog database lives outside the synced tree:
SQLite inside a folder that a sync client rewrites risks corruption and conflict copies.

## Splitting

A scanned PDF often holds several documents back to back (in the sample folder 32
PDFs hold 68 documents). The librarian splits by page ranges (ADR-0003):

1. OCR every page (ADR-0002).
2. One model call per file over the text of all pages returns the first page of each
   document.
3. Text rules check it independently: running page numbers, a repeated letterhead, the same case or contract number, a page
   that starts mid-table, printouts placed before their protocol. A strong rule that
   disagrees with the model sends the file to `Входящие/`.
4. Printouts (ultrasound frames, ECG tapes), specimen sticker sheets and patient
   diaries belong to the protocol of the same examination.

Not covered yet: documents whose pages are not contiguous in the scan, and shuffled
page order.

## Virtual collections

A document has one place in the tree, but people look for it in different ways: «all of
Dad's cardiology for the last three years», «everything for the 2026 tax deduction»,
«all documents from clinic X». A virtual collection is a saved query over the catalog.
The librarian materialises it as `Коллекции/<name>.md`, a list of links to sidecars,
regenerated on each run. Nothing is copied, so a document in five collections still
exists once; and a plain `.md` with links works in any sync client and in Obsidian,
unlike shortcuts or symlinks.

## Accounts and persons

An **account** is a user of the service and the unit of isolation (ADR-0004). It keeps the
documents of one or more **persons**: code, full name, aliases (name spellings found in
documents, used by rules to recognise the person), birth date (tells namesakes apart).
Every document is attached to one or more persons of its account. Access control
between accounts is physical separation; access rules inside an account are not needed
yet.
