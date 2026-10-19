"""Split multi-page scans into documents and score the methods.

Usage:
  python split.py <cache_dir> <split_reference.json> <out_dir>
         [--text router-google_gemini-4-argon] [--methods rules,decide,hybrid,chat]
         [--chat-model google/gemini-3.1-flash-lite]

For every boundary between consecutive pages of one PDF a method answers
"does the next page start a new document?". Methods:
  rules   text rules only (no model)
  decide  router `decide` (yes/no with probability) on the end of page A and
          the start of page B
  hybrid  a strong rule decides; otherwise `decide`
  chat    one `chat` call per file: the model returns the first pages itself
Model answers are cached in <out_dir>/cache-*.json, so reruns cost nothing.
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
from rapidfuzz import fuzz

# --- rules -------------------------------------------------------------------

# A handwritten date the owner puts on the first page of every document:
# "2.04.22", "6.12 19", "3.6.26", "31.03.2022", "61026".
HAND_DATE = re.compile(r"^\s*\d{1,2}\s*[.,/ ]\s*\d{1,2}\s*[.,/ ]?\s*(\d{2}|\d{4})\s*$|^\s*\d{5,6}\s*$")
TITLE = re.compile(
    r"протокол обследования|рекомендации по лечению|детализация по документу|"
    r"дополнительное соглашение|консультативное заключение|выписной эпикриз|"
    r"результаты холтеровского|мониторирование|направление|справка|"
    r"областная клиническая больница|онкологический диспансер|лабораторно-диагностический",
    re.I)
END = re.compile(
    r"подпись врача|^дата\s*:|\[штамп\]|проконсультироваться со специалистом|"
    r"от имени пациента|рекомендации врачей|следующее посещение|консультант|врач\s*:|"
    r"бланк разработан на основании|подпись\s*:\s*дата",
    re.I | re.M)
DEVICE = re.compile(r"SIEMENS|SM-CLIN|SonoScape|Unknown|GEMS|Kenz|MI \d|TIS \d|\d+cm\b|\bD[=:]", re.I)
# A page that starts mid-document: a table row, a lowercase letter (case matters
# here), or a known continuation block of the clinic's forms.
CONTINUES = re.compile(r"^(\||[а-яё]|(?i:назначения, запись|подпись врача|данные рентгена|рт\.ст))")
RUNNING = re.compile(r"(?:возраст\s*:?\s*\d+.{0,60}?|стр\.?\s*)(\d{1,3})\b", re.I)
CASE = re.compile(r"(случай\s*№\s*\d+|договор на оказание услуг\s*№\s*[\d ]+|номер обследования:\s*\S+)", re.I)


def lines(t: str) -> list[str]:
    return [l.strip() for l in t.splitlines() if l.strip() and set(l.strip()) - set("|-_ ")]


BRAND = re.compile(r"SIEMENS|SonoScape|GEMS|Kenz|SM-CLIN", re.I)


def cyr_words(t: str) -> int:
    return len(re.findall(r"[а-яё]{4,}", t, re.I))


def is_attachment(t: str) -> bool:
    """A device printout (ultrasound frame, ECG tape) or a sheet of specimen codes."""
    if len(DEVICE.findall(t)) >= 2 and cyr_words(t) < 15:
        return True
    return cyr_words(t) < 4 and len(re.findall(r"\b[A-Z0-9]{6,}\b", t)) >= 5


def brand(t: str) -> str | None:
    m = BRAND.search(t)
    return m.group(0).upper() if m else None


def page_no(t: str) -> int | None:
    for l in lines(t)[:3]:
        m = RUNNING.search(l)
        if m:
            return int(m.group(1))
    return None


def same_header(la: list[str], lb: list[str]) -> bool:
    """The same letterhead repeated on both pages (skipping a handwritten date)."""
    ha = [l for l in la[:4] if not HAND_DATE.match(l)][:3]
    hb = [l for l in lb[:4] if not HAND_DATE.match(l)][:3]
    # Fuzzy: OCR reads the same logo differently from page to page.
    return len(ha) == 3 and len(hb) == 3 and fuzz.ratio(" ".join(ha), " ".join(hb)) >= 90


def rules(a: str, b: str) -> tuple[bool, str, bool]:
    """Return (new_document, reason, strong)."""
    la, lb = lines(a), lines(b)
    if not lb:
        return False, "empty page B", False
    head_b = "\n".join(lb[:3])
    hand_date = bool(HAND_DATE.match(lb[0]))
    ended = bool(END.search("\n".join(la[-4:])))
    na, nb = page_no(a), page_no(b)
    if na is not None and nb == na + 1:
        return False, "running page number continues", True
    # Printouts come before the protocol they belong to.
    if is_attachment(a):
        if is_attachment(b):
            ba, bb = brand(a), brand(b)
            if ba and bb and ba != bb:
                return True, "printouts from another device", False
            return False, "printout after printout", True
        return False, "protocol after its printouts", True
    if hand_date:
        return True, "handwritten date on top of page B", True
    if is_attachment(b):
        if ended:
            return True, "printout after a finished document", False
        return False, "printout attached", False
    ca, cb = set(CASE.findall(a)), set(CASE.findall(b))
    if ca & cb:
        return False, "same case/contract number", True
    if same_header(la, lb):
        return False, "same letterhead repeated", True
    if CONTINUES.match(lb[0]):
        return False, "page B starts mid-document", True
    if TITLE.search(head_b):
        return True, "title on page B" + (" after an ending" if ended else ""), ended
    if ended:
        return True, "page A ends a document", False
    return False, "no signal", False


# --- model calls -------------------------------------------------------------

QUESTION = {
    "new_document": {
        "type": "noul",
        "instructions": (
            "These are two consecutive scanned pages from one PDF of a family medical "
            "archive: the end of page A and the start of page B. Does page B start a "
            "new, separate paper document, rather than continue the document of page A?"),
        "criteria": {
            "true": ("Page B begins another document: its own heading or clinic letterhead "
                     "after page A was finished (signature, date, closing lines)."
                     "{hand_date}"),
            "false": ("Page B continues page A: a table or text that goes on, the next page "
                      "of a numbered report, a signature page, or ultrasound/ECG printouts, "
                      "specimen sticker sheets and patient diaries that belong to the same "
                      "examination."),
        },
    }
}

CHAT_PROMPT = """Ниже текст страниц одного PDF из семейного медицинского архива, страницы по порядку.
Раздели их на отдельные бумажные документы. Правила:
- Снимки УЗИ и ленты ЭКГ, листы наклеек с номерами образцов и дневник пациента относятся к протоколу того же обследования.
{hand_date}- Документ, продолженный на следующей странице (таблица, текст, подпись), остаётся одним документом.
Ответь только JSON вида {"starts": [номера первых страниц документов]}, нумерация с 1.

"""


# The handwritten date was a habit of the owner for one batch of scans only
# (2026-10-19); --strip-hand-date measures the methods without it.
HAND_DATE_HINT = {
    "decide": " Or a handwritten date at the very top of page B.",
    "chat": "- Рукописная дата в самом верху страницы обычно стоит на первой странице документа.\n",
}


def question(strip: bool) -> dict:
    q = json.loads(json.dumps(QUESTION))
    t = q["new_document"]["criteria"]["true"]
    q["new_document"]["criteria"]["true"] = t.replace("{hand_date}", "" if strip else HAND_DATE_HINT["decide"])
    return q


def chat_prompt(strip: bool) -> str:
    return CHAT_PROMPT.replace("{hand_date}", "" if strip else HAND_DATE_HINT["chat"])


def strip_hand_date(t: str) -> str:
    """Drop a handwritten date transcribed as the first line of a page."""
    ls = t.splitlines()
    for i, l in enumerate(ls):
        if l.strip():
            if HAND_DATE.match(l):
                del ls[i]
            break
    return "\n".join(ls)


async def model_answers(pairs, files, methods, cache_dir, chat_model, strip=False):
    sfx = "-nodate" if strip else ""
    dc_path = cache_dir / f"cache-decide{sfx}.json"
    ch_path = cache_dir / f"cache-chat-{re.sub(r'[^\w.-]+', '_', chat_model)}{sfx}.json"
    dc = json.loads(dc_path.read_text(encoding="utf-8")) if dc_path.exists() else {}
    ch = json.loads(ch_path.read_text(encoding="utf-8")) if ch_path.exists() else {}
    cost = defaultdict(float)
    need_decide = {"decide", "hybrid"} & set(methods)
    headers = {"Authorization": f"Bearer {rr.os.environ['ROUTER_TOKEN']}"}
    http = create_mcp_http_client(headers=headers, timeout=None)
    async with http, streamable_http_client(rr.os.environ["ROUTER_URL"], http_client=http) as st:
        async with ClientSession(st[0], st[1]) as s:
            await s.initialize()
            if need_decide:
                for key, a, b in pairs:
                    if key in dc:
                        continue
                    res = await s.call_tool("decide", {
                        "external_user_id": "librarian", "app_name": "librarian",
                        "questions": question(strip),
                        "state": {"page_A_end": a[-1500:], "page_B_start": b[:1500]},
                    })
                    if res.is_error:
                        print("decide error", key, res.content[0].text[:200])
                        continue
                    p = rr.payload(res)
                    dc[key] = {"p": p["answers"]["new_document"]["noul"],
                               "cost": p.get("cost_usd") or p.get("cost") or 0}
                    dc_path.write_text(json.dumps(dc, indent=1), encoding="utf-8")
            if "chat" in methods:
                for sha, texts in files.items():
                    if sha in ch or len(texts) < 2:
                        continue
                    body = "\n\n".join(f"===== Страница {i} =====\n{t[:2500]}"
                                       for i, t in enumerate(texts, 1))
                    res = await s.call_tool("chat", {
                        "external_user_id": "librarian", "app_name": "librarian",
                        "model_id": chat_model, "text": chat_prompt(strip) + body,
                        "params": {"temperature": 0},
                    })
                    if res.is_error:
                        print("chat error", sha, res.content[0].text[:200])
                        continue
                    p = rr.payload(res)
                    m = re.search(r"\{.*\}", p.get("text") or "", re.S)
                    starts = json.loads(m.group(0))["starts"] if m else [1]
                    ch[sha] = {"starts": starts, "cost": p.get("cost_usd") or p.get("cost") or 0}
                    ch_path.write_text(json.dumps(ch, indent=1), encoding="utf-8")
    for v in dc.values():
        cost["decide"] += v["cost"]
    for v in ch.values():
        cost["chat"] += v["cost"]
    return dc, ch, cost


# --- scoring -----------------------------------------------------------------

def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("reference")
    ap.add_argument("out")
    ap.add_argument("--text", default="router-google_gemini-4-argon")
    ap.add_argument("--methods", default="rules,decide,hybrid,chat")
    ap.add_argument("--chat-model", default="google/gemini-3.1-flash-lite")
    ap.add_argument("--strip-hand-date", action="store_true",
                    help="remove handwritten dates from page text and from the prompts")
    args = ap.parse_args()
    rr.load_env()

    cache, out = Path(args.cache), Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    methods = args.methods.split(",")
    ref = {k: v for k, v in json.loads(Path(args.reference).read_text(encoding="utf-8")).items()
           if not k.startswith("_")}
    manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
    files: dict[str, list[str]] = defaultdict(list)
    source = {}
    for e in manifest:
        sha = e["sha256"][:12]
        t = (cache / "out" / args.text / f"{e['page_id']}.txt").read_text(encoding="utf-8")
        files[sha].append(strip_hand_date(t) if args.strip_hand_date else t)
        source[sha] = e["source"]

    pairs = []  # (key, text A, text B)
    for sha, texts in files.items():
        for k in range(1, len(texts)):
            pairs.append((f"{sha}:{k}", texts[k - 1], texts[k]))

    dc, ch, cost = ({}, {}, {})
    if {"decide", "hybrid", "chat"} & set(methods):
        dc, ch, cost = asyncio.run(model_answers(pairs, files, methods, out, args.chat_model, args.strip_hand_date))

    truth, unsure = {}, set()
    for sha, spec in ref.items():
        n = len(files[sha])
        for k in range(1, n):
            truth[f"{sha}:{k}"] = (k + 1) in spec["starts"]
        for k in spec.get("unsure", []):
            unsure.add(f"{sha}:{k}")

    pred = defaultdict(dict)
    reasons = {}
    for key, a, b in pairs:
        new, why, strong = rules(a, b)
        reasons[key] = (new, why, strong)
        pred["rules"][key] = new
        if key in dc:
            pred["decide"][key] = dc[key]["p"] >= 0.5
            pred["hybrid"][key] = new if strong else dc[key]["p"] >= 0.5
    for sha, v in ch.items():
        for k in range(1, len(files[sha])):
            pred["chat"][f"{sha}:{k}"] = (k + 1) in v["starts"]
    for sha, texts in files.items():  # single-page files have no boundaries
        pass

    rows, errors = [], defaultdict(list)
    for m in methods:
        tp = fp = fn = tn = 0
        for key, t in truth.items():
            if key in unsure or key not in pred[m]:
                continue
            p = pred[m][key]
            tp += p and t
            fp += p and not t
            fn += (not p) and t
            tn += (not p) and not t
            if p != t:
                errors[m].append(key)
        files_ok = sum(
            all(pred[m].get(f"{sha}:{k}") == truth[f"{sha}:{k}"]
                for k in range(1, len(files[sha])) if f"{sha}:{k}" not in unsure)
            for sha in ref if len(files[sha]) > 1)
        multi = sum(1 for sha in ref if len(files[sha]) > 1)
        prec = tp / (tp + fp) if tp + fp else 0
        rec = tp / (tp + fn) if tp + fn else 0
        rows.append((m, tp, fp, fn, tn, prec, rec, files_ok, multi, cost.get(m if m != "hybrid" else "decide", 0)))

    md = ["| Метод | Точность границ | Полнота границ | Лишние разрезы | Пропущенные | Файлов без ошибок | $ |",
          "|---|---|---|---|---|---|---|"]
    for m, tp, fp, fn, tn, prec, rec, ok, multi, c in rows:
        md.append(f"| {m} | {prec:.0%} | {rec:.0%} | {fp} | {fn} | {ok} из {multi} | {c:.4f} |")
    md += ["", "Ошибки по методам (файл, граница после страницы k, правда):"]
    for m in methods:
        md.append(f"\n**{m}**")
        for key in errors[m]:
            sha, k = key.split(":")
            extra = ""
            if m in ("rules", "hybrid"):
                extra = f" — правило: {reasons[key][1]}"
            if key in dc:
                extra += f" — decide p={dc[key]['p']:.2f}"
            md.append(f"- {source[sha]} p{k}|p{int(k) + 1}: правда {'новый' if truth[key] else 'тот же'}{extra}")
    (out / f"split-scores-{args.text}{'-nodate' if args.strip_hand_date else ''}.md").write_text("\n".join(md) + "\n", encoding="utf-8")
    print("\n".join(md[:2 + len(rows)]))
    print(f"boundaries scored: {sum(1 for k in truth if k not in unsure)}, new: {sum(1 for k, v in truth.items() if v and k not in unsure)}")


if __name__ == "__main__":
    main()
