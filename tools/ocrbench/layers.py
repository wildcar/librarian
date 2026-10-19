"""Essential text and de-identified summaries (ADR-0005): run models, check them.

Usage:
  python layers.py <cache_dir> <split_reference.json> <persons.json> <out_dir>
         --models id1,id2 [--limit N] [--workers 4]

Documents are the hand-labelled split of the sample (split_reference.json) over the
Gemini 4 Argon page text. For every model:
  essential  the model sees numbered lines and returns the numbers to drop, with a
             category; the essential text is the kept lines, so it cannot reword.
  summary    the model writes a de-identified summary from the rule-cleaned text
             (the same input for every model, to compare summarising alone).
Checks: essential — share removed, known boilerplate left, suspicious removals
(a dropped line with digits or medical words that is not known boilerplate);
summary — leaks (names, ids, organisations, contacts), numbers absent from the
source. Results: <out_dir>/<model>/<doc>.md, cache-*.json, layers-scores.md.
"""
import argparse
import asyncio
import json
import re
from collections import defaultdict
from pathlib import Path

import run_router as rr
from mcp import ClientSession
from mcp.client.streamable_http import create_mcp_http_client, streamable_http_client

# --- documents -----------------------------------------------------------------

def build_docs(cache: Path, ref_path: Path, text_engine: str) -> list[dict]:
    ref = {k: v for k, v in json.loads(ref_path.read_text(encoding="utf-8")).items()
           if not k.startswith("_")}
    manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
    pages, source = defaultdict(list), {}
    for e in manifest:
        sha = e["sha256"][:12]
        pages[sha].append((cache / "out" / text_engine / f"{e['page_id']}.txt").read_text(encoding="utf-8"))
        source[sha] = e["source"]
    docs = []
    for sha in sorted(pages, key=lambda s: source[s]):
        starts = ref[sha]["starts"] + [len(pages[sha]) + 1]
        for i in range(len(starts) - 1):
            a, b = starts[i], starts[i + 1] - 1
            text = "\n".join(pages[sha][a - 1:b])
            lines = [l.rstrip() for l in text.splitlines() if l.strip()]
            docs.append({"id": f"{sha}-d{i + 1}", "source": source[sha], "pages": f"{a}-{b}", "lines": lines})
    return docs


# --- rules -----------------------------------------------------------------------

BOILERPLATE = re.compile(
    r"бланк разработан|приказом мз|лицензи|огрн|инн\s*/?\s*кпп|скидк|онлайн.?запис|"
    r"подробности по телефону|имеются противопоказания|проконсультироваться со специалистом|"
    r"smclinic|www\.|https?://|e-?mail|telegram|почта\s*:|звонок\s*:|штрихкод|"
    r"система менеджмента качества|круглосуточн\w* запис|многопрофильный медицинский холдинг|"
    r"тел\.?\s*/\s*факс|@\w|для справок|документ подписан электронной подписью|сертификат\s*:",
    re.I)
MEDICAL = re.compile(
    r"диагноз|заключени|рекоменд|назнач|принимать|\bмг\b|\bмл\b|ммоль|мкмоль|уд/мин|\bмм\b|\bсм\b|"
    r"результат|анализ|норм|стеноз|кровото|экг|узи|холестерин|гемоглобин|давлени|"
    r"\bприем\b|таб\.|капс|терапи|операци|осмотр|жалоб",
    re.I)


def rules_clean(lines: list[str]) -> list[str]:
    return [l for l in lines if not BOILERPLATE.search(l)]


FIO = re.compile(r"\b([А-ЯЁ][а-яё]+(?:-[А-ЯЁ][а-яё]+)?)\s+([А-ЯЁ][а-яё]+)\s+([А-ЯЁ][а-яё]+(?:вич|вна|ична|чна|ич))\b")
INITIALS = re.compile(r"\b([А-ЯЁ][а-яё]{3,})\s+[А-ЯЁ]\.\s?[А-ЯЁ]\.|[А-ЯЁ]\.\s?[А-ЯЁ]\.\s?([А-ЯЁ][а-яё]{3,})\b")
ORG = re.compile(r"(?:ООО|ГБУ(?:\s+РО)?|ГБУЗ|АО|ЗАО|ФГБУ)\s*[«\"„]+\s*([^»\"“]{3,60})[»\"“]")
BRAND = re.compile(r"\b[А-ЯЁA-Z]{2,}-[А-ЯЁA-Z][а-яёa-z]+\b")
PATTERNS = {
    "phone": re.compile(r"(?:\+7|8)[\s(-]*\d{3,4}[\s)-]*\d{1,3}[\s-]*\d{2}[\s-]*\d{2}"),
    "email": re.compile(r"[\w.-]+@[\w-]+\.\w+"),
    "card_or_case": re.compile(r"\b[А-ЯA-Z]{1,3}\d{6,}\b|\b\d{2}[МM]/\d{3,4}\b|№\s*\d[\d\s-]{3,}"),
    "street": re.compile(r"\bул\.?\s+[А-ЯЁ][а-яё]+|\bулица\s+[А-ЯЁ]"),
}


def stem(word: str) -> str:
    w = word.lower()
    return w[:-2] if len(w) > 6 else w


def forbidden(lines: list[str], persons: dict) -> set[str]:
    text = "\n".join(lines)
    out = set()
    for m in FIO.finditer(text):
        out.update({stem(m.group(1)), f"{m.group(2)} {m.group(3)}".lower()})
    for m in INITIALS.finditer(text):
        out.add(stem(m.group(1) or m.group(2)))
    for m in ORG.finditer(text):
        out.add(m.group(1).strip().lower())
    for m in BRAND.finditer(text):
        out.add(m.group(0).lower())
    for p in persons.values():
        out.update(s.lower() for s in [p["full_name"], *p["aliases"], *p["birth_date"], *p["ids"]])
    common = {"прием", "приема", "протокол", "клиника", "врача", "пациент"}
    return {s for s in out if len(s) >= 4 and s not in common}


def leaks(summary: str, banned: set[str]) -> list[str]:
    low = summary.lower()
    hits = [f"name/org: {s}" for s in sorted(banned) if s in low]
    for kind, rx in PATTERNS.items():
        hits += [f"{kind}: {m.group(0)}" for m in rx.finditer(summary)]
    return hits


DATE = re.compile(r"\d{1,2}[./]\d{1,2}[./]\d{2,4}")
NUM = re.compile(r"\d+(?:[.,:]\d+)*")


MONTHS = {m: i for i, m in enumerate(
    "январ феврал март апрел ма июн июл август сентябр октябр ноябр декабр".split(), 1)}
NUM_DATE = re.compile(r"\b(\d{1,2})\s*[./]\s*(\d{1,2})\s*[./]\s*(\d{2}|\d{4})\b")
WORD_DATE = re.compile(r"\b(\d{1,2})\s+([а-яё]+)\s+(\d{4})", re.I)


def dates(text: str) -> set[tuple[int, int, int]]:
    """Dates as (day, month, two-digit year), whatever the spelling."""
    out = {(int(d), int(m), int(y) % 100) for d, m, y in NUM_DATE.findall(text)}
    for d, word, y in WORD_DATE.findall(text):
        m = next((v for k, v in MONTHS.items() if word.lower().startswith(k)), None)
        if m:
            out.add((int(d), m, int(y) % 100))
    return out


def invented_numbers(summary: str, source: str) -> list[str]:
    src = {n.replace(",", ".") for n in NUM.findall(source)}
    src_digits = re.sub(r"\D", "", source)
    src_dates = dates(source)
    out = []
    for m in NUM_DATE.finditer(summary):
        if (int(m.group(1)), int(m.group(2)), int(m.group(3)) % 100) not in src_dates:
            out.append(m.group(0))
    summary = NUM_DATE.sub(" ", summary)
    for n in NUM.findall(summary):
        k = n.replace(",", ".")
        if len(re.sub(r"\D", "", k)) < 2:
            continue
        if k in src or re.sub(r"\D", "", k) in src_digits:
            continue
        out.append(n)
    return out


# --- prompts ---------------------------------------------------------------------

ESSENTIAL_PROMPT = """Ниже текст медицинского документа, распознанный со скана. Строки пронумерованы.
Найди строки, которые не несут смысла для читателя документа, и только их:
- служебные пометки бланка («Бланк разработан на основании формы…», лицензии, ОГРН, ИНН);
- реклама и акции, призывы записаться;
- контакты: сайты, телефоны для записи, почта, мессенджеры, адреса клиники в колонтитулах;
- шапки и колонтитулы, повторённые на второй и следующих страницах;
- цифры штрихкодов, служебные строки аппаратов на распечатках (MI, TIS, частота, масштаб);
- пустые поля бланка (подчёркивания, «___»).
Не трогай строки с медицинскими данными, результатами, назначениями, датами, ФИО пациента и врача, первое упоминание клиники. Сомневаешься — оставь.
Ответь только JSON: {"remove": [[номер строки, "категория"], ...]}

"""

SUMMARY_PROMPT = """Ниже текст медицинского документа. Напиши его резюме на русском, 3–8 предложений.
Первое предложение: какой это документ и от какой даты.
Дальше: что делали, что нашли, диагнозы, назначения и ключевые значения с единицами, как в документе.
Строгие правила о персональных данных:
- пациента называй «пациент», без имени, фамилии, инициалов и даты рождения;
- врачей называй только по специальности («кардиолог»), без фамилий;
- клинику называй только по типу («частная клиника», «областная больница»), без названия, адреса, города и телефонов;
- никаких номеров карт, случаев, договоров, полисов, штрихкодов.
Не добавляй ничего, чего нет в тексте. Ответь только текстом резюме.

"""


async def ask(s, model, prompt):
    res = await s.call_tool("chat", {
        "external_user_id": "librarian", "app_name": "librarian",
        "model_id": model, "text": prompt, "params": {"temperature": 0},
    })
    if res.is_error:
        raise RuntimeError(res.content[0].text[:300])
    p = rr.payload(res)
    return p.get("text") or "", p.get("cost_usd") or p.get("cost") or 0


async def run_model(model, docs, out, workers):
    path = out / f"cache-{re.sub(r'[^\w.-]+', '_', model)}.json"
    cache = json.loads(path.read_text(encoding="utf-8")) if path.exists() else {}
    sem = asyncio.Semaphore(workers)
    headers = {"Authorization": f"Bearer {rr.os.environ['ROUTER_TOKEN']}"}
    http = create_mcp_http_client(headers=headers, timeout=None)
    async with http, streamable_http_client(rr.os.environ["ROUTER_URL"], http_client=http) as st:
        async with ClientSession(st[0], st[1]) as s:
            await s.initialize()

            async def one(d):
                c = cache.setdefault(d["id"], {})
                async with sem:
                    try:
                        if "remove" not in c:
                            numbered = "\n".join(f"{i}: {l}" for i, l in enumerate(d["lines"], 1))
                            text, cost = await ask(s, model, ESSENTIAL_PROMPT + numbered)
                            m = re.search(r"\{.*\}", text, re.S)
                            c["remove"] = json.loads(m.group(0))["remove"] if m else []
                            c["cost_essential"] = cost
                        if "summary" not in c:
                            src = "\n".join(rules_clean(d["lines"]))
                            text, cost = await ask(s, model, SUMMARY_PROMPT + src)
                            c["summary"] = text.strip()
                            c["cost_summary"] = cost
                    except Exception as e:  # keep going; rerun fills the gaps
                        print(model, d["id"], "ERROR", str(e)[:200], flush=True)
                    path.write_text(json.dumps(cache, ensure_ascii=False, indent=1), encoding="utf-8")

            await asyncio.gather(*(one(d) for d in docs))
    return cache


# --- scoring -----------------------------------------------------------------------

def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("reference")
    ap.add_argument("persons")
    ap.add_argument("out")
    ap.add_argument("--models", required=True)
    ap.add_argument("--text", default="router-google_gemini-4-argon")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--score-only", action="store_true")
    args = ap.parse_args()
    rr.load_env()

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    persons = {k: v for k, v in json.loads(Path(args.persons).read_text(encoding="utf-8")).items()
               if not k.startswith("_")}
    docs = build_docs(Path(args.cache), Path(args.reference), args.text)
    if args.limit:
        docs = docs[:args.limit]
    models = args.models.split(",")

    caches = {}
    for m in models:
        if args.score_only:
            p = out / f"cache-{re.sub(r'[^\w.-]+', '_', m)}.json"
            caches[m] = json.loads(p.read_text(encoding="utf-8")) if p.exists() else {}
        else:
            caches[m] = asyncio.run(run_model(m, docs, out, args.workers))

    rows, details = [], defaultdict(list)
    for m in models:
        c = caches[m]
        tot_chars = kept_chars = boiler_left = boiler_total = 0
        suspicious, leak_docs, leak_hits, inv_docs, inv_nums, done, cost = [], 0, 0, 0, 0, 0, 0.0
        restored = 0
        sum_len = []
        mdir = out / re.sub(r"[^\w.-]+", "_", m)
        mdir.mkdir(exist_ok=True)
        for d in docs:
            r = c.get(d["id"], {})
            if "remove" not in r or "summary" not in r:
                continue
            done += 1
            cost += (r.get("cost_essential") or 0) + (r.get("cost_summary") or 0)
            drop = {}
            for item in r["remove"]:
                n, cat = (item[0], item[1] if len(item) > 1 else "") if isinstance(item, list) else (item, "")
                if isinstance(n, int) and 1 <= n <= len(d["lines"]):
                    drop[n] = cat
            # Safeguard (ADR-0005): when every copy of a repeated line was dropped, the
            # first copy comes back — a running header may be the only place a fact is.
            shape = lambda l: re.sub(r"[\d\s]+", " ", l).strip().lower()  # noqa: E731
            groups = defaultdict(list)
            for i, l in enumerate(d["lines"], 1):
                groups[shape(l)].append(i)
            for idx in groups.values():
                if len(idx) > 1 and all(i in drop for i in idx) and not BOILERPLATE.search(d["lines"][idx[0] - 1]):
                    del drop[idx[0]]
                    restored += 1
            kept = [l for i, l in enumerate(d["lines"], 1) if i not in drop]
            tot_chars += sum(len(l) for l in d["lines"])
            kept_chars += sum(len(l) for l in kept)
            # A dropped line is safe when the same line (digits aside) is still in the
            # essential text: a repeated letterhead or running header.
            kept_shapes = {re.sub(r"[\d\s]+", " ", l).strip().lower() for l in kept}
            for i, l in enumerate(d["lines"], 1):
                if BOILERPLATE.search(l):
                    boiler_total += 1
                    boiler_left += i not in drop
                elif (i in drop and re.sub(r"[\d\s]+", " ", l).strip().lower() not in kept_shapes
                      and (MEDICAL.search(l) or DATE.search(l)
                           or (re.search(r"\d", l) and len(l) >= 30))):
                    suspicious.append((d["id"], i, drop[i], l))
            src = "\n".join(d["lines"])
            lk = leaks(r["summary"], forbidden(d["lines"], persons))
            inv = invented_numbers(r["summary"], src)
            leak_docs += bool(lk)
            leak_hits += len(lk)
            inv_docs += bool(inv)
            inv_nums += len(inv)
            sum_len.append(len(r["summary"]))
            if lk:
                details[m].append(f"- УТЕЧКА {d['id']} ({d['source']} стр. {d['pages']}): {'; '.join(lk)}")
            if inv:
                details[m].append(f"- ЧИСЛА {d['id']}: нет в исходнике: {', '.join(inv)}")
            (mdir / f"{d['id']}.md").write_text(
                f"# {d['source']}, стр. {d['pages']}\n\n## Резюме\n\n{r['summary']}\n\n"
                f"Утечки: {lk or 'нет'}\nЧисла не из исходника: {inv or 'нет'}\n\n"
                f"## Суть ({sum(len(l) for l in kept)} из {sum(len(l) for l in d['lines'])} знаков)\n\n"
                + "\n".join(kept) + "\n\n## Удалено\n\n"
                + "\n".join(f"- [{drop[i]}] {l}" for i, l in enumerate(d["lines"], 1) if i in drop) + "\n",
                encoding="utf-8")
        for sid, i, cat, l in suspicious:
            details[m].append(f"- ПОДОЗРИТЕЛЬНО удалено {sid} строка {i} [{cat}]: {l[:140]}")
        rows.append((m, done, 1 - kept_chars / tot_chars if tot_chars else 0,
                     boiler_left, boiler_total, len(suspicious), leak_docs, leak_hits,
                     inv_docs, inv_nums, sum(sum_len) / len(sum_len) if sum_len else 0, cost, restored))

    md = ["| Модель | Документов | Убрано из текста | Мусор оставлен | Возвращено страховкой | Подозрительных удалений | "
          "Резюме с утечкой | Утечек всего | Резюме с чужими числами | Длина резюме | $ всего | $ на документ |",
          "|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for m, done, red, bl, bt, sus, ld, lh, idoc, inum, slen, cost, rest in rows:
        md.append(f"| {m} | {done} | {red:.0%} | {bl} из {bt} | {rest} | {sus} | {ld} | {lh} | {idoc} ({inum} чисел) | "
                  f"{slen:.0f} | {cost:.3f} | {cost / done if done else 0:.4f} |")
    for m in models:
        md += ["", f"### {m}", ""] + details[m]
    (out / "layers-scores.md").write_text("\n".join(md) + "\n", encoding="utf-8")
    print("\n".join(md[:2 + len(rows)]))


if __name__ == "__main__":
    main()
