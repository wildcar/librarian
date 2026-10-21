# ADR-0006: Batches — the person and characteristics given to everything that came in together

**Status:** Accepted (2026-10-21)
**Date:** 2026-10-21
**Deciders:** wildcar
**Scope:** catalog schema (batches, document characteristics), `librarian ingest`, `librarian batch`, `librarian index`, MCP output and search (ADR-0004)

---

## Context

Documents are linked to persons only by rules that find a person's name, alias or birth
date in the text (`librarian index`). On the sample 8 of 68 documents stay unlinked: four
consultation forms carry the name in handwriting that OCR misreads («Евтушенко П. В.»,
«Е В», «О В», «БВ»), one ECG has a misspelt handwritten surname, three carry no name at
all. Smarter matching (surname plus birth year) would fix some of them and none of the
rest.

The owner knows more than the text: a folder of father's scans is father's, a set of
tickets belongs to one trip. The owner's position (2026-10-21):

- when a batch is sent, it must be possible to say whose documents they are — an
  existing person or a new one — and to give the batch more characteristics;
- the library is general-purpose: medicine is the first example, tickets, car
  insurance, contracts, notes and appliance manuals follow; characteristics are an
  open set that differs by kind of document, not a fixed list of columns;
- a document missing a fact stays in circulation with less information; later work only
  refines it.

Until now a batch existed only as a row in `intake_runs` (folder and time); which files
came with which run was not recorded.

## Decision

**A batch** is what came into the account at one time: one `librarian ingest` run over a
folder now; an upload through `add_document` or a cloud link later. It holds originals
(by sha256); one original may belong to several batches.

- A folder run makes a batch of the originals that were new to the account in that run.
  When the run is given a person or characteristics, the batch holds every original the
  run saw: the owner has labelled that folder as a whole. A run that brings nothing new
  and labels nothing makes no batch, so periodic rescans do not pile up empty batches.
- The existing intake runs become batches with the same id (migration).

**A batch carries** persons (codes; several are allowed for household papers) and
characteristics: pairs «key = value», keys and values free text, a key may have several
values (`тег=страховка`, `тег=машина`). Keys are compared lower-cased and trimmed.

```
batches        (id, kind, root, label, created)          kind: folder | upload | link
batch_originals(batch_id, sha256)
batch_persons  (batch_id, code)
batch_attrs    (batch_id, key, value)
doc_attrs      (doc_id, key, value, source)              source: batch:<id> | manual | …
doc_persons    (…, method)                               method: rule | batch | manual
```

**Inheritance** happens in `librarian index`, which already recomputes everything derived
without model calls: every document cut from a batch's original gets the batch's persons
(`method = batch`, reason «пачка N») and characteristics (`source = batch:N`). Changing a
batch later (`librarian batch set`) reruns `index`, so its documents follow at once.

**Precedence:** manual > batch > rule. A manual link or characteristic is never
overwritten. When a rule finds a person in a document whose batch names other persons,
both links stay and the document gets a `review` reason («человек: в тексте найден не
тот человек, что у пачки») — a stray document of another family member in father's folder
is not hidden by the batch.

**Interfaces:**

- `librarian ingest --account A [--person CODE]... [--name … --label … --birth … --alias …]
  [--attr key=value]... [--batch-label …] <folder>`; with `--name`, an unknown `--person`
  code is created as a new person.
- `librarian batch list --account A` and `librarian batch set --account A <id>
  [--person CODE]... [--no-person] [--attr key=value]... [--unset key[=value]]...`.
- MCP: documents return `attrs` (key → values) and `batches`; `search_documents` takes
  `attrs` (["key=value"], all must hold), `batch`, and `missing` accepts any
  characteristic key besides `date` and `person`.

## Consequences

- Linking no longer depends on reading a name: everything that came in a labelled batch is
  linked, including documents with no name in them.
- Characteristics give tags and per-kind facts (trip, insurer, appliance) a place without
  schema changes; the model-extracted characteristics planned next use the same table with
  their own `source`.
- A wrong batch label spreads to every document of the batch; `review` catches the case
  where the text disagrees, and `batch set` fixes the whole batch in one step.
- Documents keep no copy of batch data, only the derived rows `index` rebuilds, so a batch
  is changed in one place.

## Alternatives considered

- **Surname plus birth year as a weak rule** — covers 4 of the 8 sample documents and
  nothing without a name; the owner chose batches as the general mechanism. It may still
  come later as a rule, under batches in precedence.
- **A batch per folder path** — a folder that grows would make new files inherit
  silently, and an upload has no folder; a batch per intake keeps what the owner said
  about one delivery attached to that delivery.
- **Fixed columns for characteristics** — every new kind of document (tickets, manuals,
  photos) would need a migration; rejected by the owner.
- **A settings file inside the source folder** — sources are read-only and uploads have
  no folder.
