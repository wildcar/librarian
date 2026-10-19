# ADR-0005: Text layers — essential text for analysis, de-identified summaries by default

**Status:** Accepted (2026-10-19)
**Date:** 2026-10-19
**Deciders:** wildcar
**Scope:** extraction and output stages; sidecar format (`docs/filing.md`); MCP tools (ADR-0004)

---

## Context

The user reaches the archive through an AI assistant of their choice (ADR-0004). Every
text an MCP tool returns goes into that assistant's context and to its vendor. Most
questions ("when was the last echo?", "what did the cardiologist prescribe?") need the
medical content, not who the patient, the doctor or the clinic were.

Scanned documents are also full of text that carries no meaning for the reader: form
approval notes («Бланк разработан на основании формы № 014-1/у, утвержденной Приказом
МЗ России от 14 апреля 2025 г. № 207н»), advertising («Скидка 20% на эндоскопию после
15:00»), contact blocks, letterheads repeated on every page, barcode digits, empty form
fields. It costs tokens and buries the content.

The owner's position (2026-10-19): the original is needed only to check something
against the primary source; analysis and answers need an essential text copy with the
insignificant information removed; most requests should use a summary of the document
without personal information about the patient, the doctor or the clinic.

## Decision

Every document has four layers.

| Layer | What it is | Stored | Returned by MCP |
|---|---|---|---|
| Original | the source file (pages of a scan) | not stored: `source_uri` + sha256; fetched on demand and checked against the hash. Files uploaded through `add_document` are stored, there is no other copy | `get_file`, for verification only |
| Verbatim text | OCR output as read (ADR-0002) | yes, internal | only on explicit request |
| Essential text («суть») | the verbatim text with insignificant parts removed | yes | `get_document(layer="essential")` |
| Summary | a short de-identified summary | yes | **default** for `search_documents` and `get_document` |

**Essential text** is produced by removal only, never by rewording: every number, date,
name, measurement, diagnosis and prescription stays exactly as read. Removed: form
approval notes and legal boilerplate, advertising, contact blocks and websites,
letterheads repeated after the first page, barcode and stock digits, empty form fields,
device service lines on printouts. Known boilerplate is removed by rules; a model handles
the rest. A check compares the two texts: any removed fragment that contains a digit, a
date or a medical term outside the known boilerplate is restored and the document is
flagged.

**Summary** is written by a model from the essential text and contains no personal data:

- the patient appears as the person's code or as «пациент»;
- doctors appear by specialty («кардиолог»), never by name;
- clinics appear by kind («частная клиника», «областная больница»), never by name or address;
- no birth dates, card, case, contract or policy numbers, phones, e-mails, addresses;
- kept: document type, document date, examinations, findings, diagnoses, prescriptions,
  measurements and their values.

A rule check runs on every summary: the person's names and aliases, names found in the
document's metadata, and patterns of phones, e-mails, card and policy numbers must not
occur. A hit means regenerate once, then flag.

**Structured facts** (document date, persons, type, specialty, institution, doctor) live
in the catalog with provenance (SPEC principle 3). Filters in `search_documents` work on
them without exposing text. Whether a tool returns the institution or doctor name is
decided per field: by default it returns the de-identified value.

## Consequences

- The default path to the assistant carries no names, numbers or addresses, at the cost
  of one summary call per document.
- Two extra model passes per document (essential text, summary). With Gemini-class
  models this is cents per hundred pages; the exact model and price are to be measured
  on the sample, like ADR-0002 and ADR-0003.
- Keeping the verbatim text means improved cleaning or summarising can be rerun without
  paying for OCR again.
- Not storing originals of link sources means verification depends on the source being
  reachable; a moved or edited file is reported as such, not silently trusted.
- The sidecar format in `docs/filing.md` changes: `## Резюме` (de-identified) and
  `## Суть` (essential text); the verbatim text stays in account storage.

## Alternatives considered

- **Return the full text by default** — simplest, but every question sends patient,
  doctor and clinic names to the assistant's vendor.
- **Summaries only, no essential text** — summaries lose detail (exact values, wording of
  a prescription); questions that need it would fall back to the noisy verbatim text.
- **Rewrite instead of removing for the essential text** — shorter, but a rewrite can
  change a value or a diagnosis; removal-only keeps the copy checkable against the
  verbatim text.
- **Pseudonymise names with stable placeholders in all layers** — useful later for
  cross-document questions about the same doctor; not needed for the first version.
