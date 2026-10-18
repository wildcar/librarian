# Agent Instructions

Primary entrypoint for any agent (Claude, Codex, DeepSeek, etc.) working in this repository.

Keep this file under ~200 lines — it is loaded into context every session. Details belong
in `AGENTS/` docs; repeatable procedures belong in skills under `.claude/skills/`.

## Project

librarian — a pedantic librarian that ingests local and cloud documents, analyses them (rules first, models via model-router-mcp) and files them into an ordered archive shared by a family.

## Environment

- OS / shell: see `AGENTS/ENV.md`
- Commit identity: `wildcar <wildcar@mail.ru>`
- Details, credentials, command cheat-sheet: `AGENTS/ENV.md`

## Document Map

| File | Role |
|------|------|
| `AGENTS.md` | This entrypoint. Workflow, rules, map. |
| `CLAUDE.md` | Compatibility pointer to `AGENTS.md`. |
| `AGENTS/SPEC.md` | Functional specification — source of truth for product behavior. |
| `AGENTS/STATE.md` | Current snapshot: goal, now, next, open questions, deferred. Overwritten each iteration. |
| `AGENTS/HISTORY.md` | Append-only iteration log, newest first. Read only the top few entries. |
| `AGENTS/MEMORY.md` | Durable cross-session memory: working agreements + project facts. The ONLY agent memory store — see Memory. |
| `AGENTS/ENV.md` | Host, tools, credentials, command cheat-sheet. Read on demand. |
| `README.md` | Public-facing readme: what the project is, how to run it locally. |
| `docs/` | Domain / reference docs. Read on demand when a task touches that area. |
| `docs/adr/` | Architecture Decision Records — one file per significant decision (see `docs/adr/TEMPLATE.md`). |

## Startup Checklist

1. Read `AGENTS.md` (this file).
2. Read `AGENTS/SPEC.md`.
3. Read `AGENTS/STATE.md`.
4. Read top 3–5 entries in `AGENTS/HISTORY.md`.
5. Read `AGENTS/MEMORY.md` (working agreements + durable facts).
6. Check `git status --short` before editing; do not overwrite unrelated user changes.

Open `AGENTS/ENV.md` only when you need environment details. Open the relevant file under `docs/` when the task touches that domain.

## Change Workflow

For every iteration that changes code or behavior:

1. If the functional contract changes — update `AGENTS/SPEC.md` first.
2. Make the changes.
3. Overwrite `AGENTS/STATE.md` to reflect the new current state.
4. Prepend a new entry to `AGENTS/HISTORY.md` using the format below.
5. Commit and push once the Definition of Done (below) is met.

### `AGENTS/HISTORY.md` entry format (≤5 lines, newest first)

```
## YYYY-MM-DD · <short iteration title>
- What: <one line — what changed>
- Why: <one line — reason / task>
- Files: <key paths, comma-separated>
- Next: <one line — what was planned right after>
```

Keep each entry tight. Long explanations belong in commit messages or `SPEC.md`. An optional `Fixes-on-the-fly:` line is fine when an iteration also corrected small things discovered mid-way.

When `HISTORY.md` grows past ~200 lines, move the oldest entries to `AGENTS/HISTORY-ARCHIVE.md` (create it on demand) and keep the recent ones here.

When you ship a deferred item from `STATE.md`, write a normal HISTORY entry and remove the item from `STATE.md`.

Parallel work: only the top-level session writes `STATE.md` and `HISTORY.md`. Subagents and secondary sessions report back instead of editing these files.

## Definition of Done

An iteration is done only when:

1. Build succeeds.
2. Lint / typecheck is clean.
3. Tests pass.
4. SPEC / STATE / HISTORY (and MEMORY, if a durable fact emerged) are updated.

Commands for 1–3 live in Stack & Commands. A step that does not apply yet (e.g. the project has no test suite) goes to `AGENTS/STATE.md` → Deferred instead of being silently skipped.

## Memory

`AGENTS/MEMORY.md` is the **single** store of durable agent memory in this project.
Do not use external or per-tool memory stores (memory directories outside the repo, a
tool's built-in memory, etc.): memory must travel with the repository when cloned.

- Read `AGENTS/MEMORY.md` at the start of every session (see Startup Checklist).
- When you learn a durable fact or a working agreement, append a short bullet there and
  commit it together with the related change.
- Split of concerns: durable facts/agreements -> `MEMORY.md`; current snapshot ->
  `STATE.md`; iteration log -> `HISTORY.md`.

Recording rules — keep these a habit:

- One bullet = one fact; keep it short. Long explanations belong in commit messages or `SPEC.md`.
- For working agreements, add a brief **why** so the rule doesn't look arbitrary.
- Convert relative dates to absolute ("today" → the concrete date).
- Do NOT record what is already in the code, git history, or SPEC/STATE/HISTORY.
- Consolidate from time to time: merge duplicates, drop stale or wrong entries.

## Language Rules

- Source code, technical docs, code comments: English.
- Conversation with the user: Russian.
- End-user UI text: Russian, with ability to extend to other languages.
- Russian prose for humans (replies to the user, UI copy, user-facing docs, reports, release notes) follows Communication & Writing Style below.
- Existing docs already written in another language are an established contract — keep editing them in that language; don't silently translate.

## Communication & Writing Style

Keep answers short and to the point, no filler. We're having a dialogue: I ask or say something, you respond. First answer the question directly; optionally add one alternative or a follow-up question to continue the conversation. If you use specialized technical terms or jargon, add a brief explanation in parentheses. Prefer a thoughtful, general, correct solution over quick fixes, hacks, or hardcoding.

Пиши как живой человек. Смысл, факты, цифры и примеры сохраняй, режь воду и штампы.

Запрещено (нашёл у себя в тексте - перепиши):
- Негативные параллелизмы: «не просто X, а Y», «не только X, но и Y», «это не просто...».
- Правило трёх: три однородных слова подряд ради видимости полноты («качество, надёжность и эффективность»).
- Анонсы и переходы: «важно отметить», «стоит подчеркнуть», «давайте разберёмся», «вот что нужно знать».
- Резюмирующие концовки: «подводя итог», «в заключение», «таким образом» в финале. Концовка даёт новое или текст просто заканчивается.
- Артефакты чатбота: «отличный вопрос», «вы совершенно правы», «надеюсь, это поможет», «дайте знать, если...».
- Фальшивая доверительность и псевдоглубина: «честно?», «скажу прямо», «вот в чём штука», «по сути», «настоящий вопрос в том».
- Формулы афоризмов: «X - это язык Y», «X - валюта Z», «X - новый Y».
- Рубленый драматизм: стопки коротких фрагментов подряд («Без X. Без Y. Только Z.»). Одно короткое предложение для акцента можно, три подряд нельзя.
- Риторические вопросы.
- Хвосты-деепричастия для видимости глубины: «...подчёркивая», «...отражая», «...способствуя».
- AI-словарь: ключевой, важнейший, знаменует, символизирует, подчёркивает, демонстрирует, способствует, обеспечивает, формирует, яркий, богатый (переносн.), уникальный, потрясающий.
- Канцелярит: «является», «представляет собой», «выступает в роли», «осуществлять», «оказывать помощь», «проводить работу». Вместо них «это» или простой глагол (помогать, работать).
- Стоп-слова и усилители: «в современном мире», «на сегодняшний день», «как известно», «очень», «действительно», «по-настоящему», «безусловно». Удалил слово, смысл не изменился - значит мусор.
- Размытые атрибуции: «по мнению экспертов», «аналитики отмечают». Либо конкретный источник, либо утверждение от себя.
- Знаки вместо слов: = → > < + vs & ≈. Пиши словами.
- Техножаргон без пояснения: итерация, валидация, оптимизировать, кейс, флоу, функционал. Пиши для умного 12-летнего.
- Эмодзи, жирный на каждом слове, списки вида «**Заголовок:** пересказ заголовка».
- Догадки вместо данных: если не знаешь - скажи, что не знаешь.

Как писать:
- Глагол вместо отглагольного существительного, актив вместо пассива.
- Оценку меняй на факт: не «быстрый», а «отвечает за секунду».
- Чередуй короткие и длинные предложения. Одно предложение - одна мысль, один абзац - одна тема, главная мысль в начале абзаца.
- Имей мнение, признавай смешанные чувства, используй «я», где уместно.
- Не трогай цитаты, имена, названия и конкретные странные детали.
- Отвечай на вопрос самым применимым вариантом, не накидывай несколько, можешь обозначить наличие альтернативы одной короткой фразой.

Перед выдачей перечитай и спроси себя: «Что здесь ещё выдаёт модель?» Найденное перепиши.

## Project Rules

Hard constraints and invariants this project must not violate. Keep each rule one line.

- Never modify or delete a user's original files during analysis; changes to files are a plan first, applied only on confirmation.
- All model calls go through model-router-mcp; no direct provider SDKs or API keys in this repo.
- Every stored fact about a document records its provenance (rule or model id, time, confidence).

## Stack & Commands

Go (ADR-0001, Proposed), SQLite catalog, MCP client to model-router-mcp. Full cheat-sheet: `AGENTS/ENV.md`.

```bash
go mod download                    # install
go run ./cmd/librarian version     # run
go build ./...                     # build
go test ./...                      # test
go vet ./... && gofmt -l .         # lint (gofmt must print nothing)
```

## Architecture

Map of the codebase so an agent knows where things live.

```
cmd/librarian/   CLI entrypoint
internal/        application packages (pipeline stages, catalog, router client)
docs/adr/        architecture decisions
```

## Code Style

- `gofmt` formatting, standard Go project layout (`cmd/`, `internal/`).
- Errors are wrapped with context (`fmt.Errorf("...: %w", err)`), never swallowed.
- Match the conventions of surrounding code: comment density, naming, idiom.
