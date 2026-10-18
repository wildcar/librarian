"""Score OCR engines against hand-read key facts.

Usage: python score.py <cache_dir> <reference.json> <out_dir>

A fact is a short string read by eye from the page. Both the fact and the
engine output are normalised (case, ё, punctuation, whitespace, Latin/Cyrillic
look-alikes). Exact substring = 1 point, fuzzy partial match >= 85 = 0.5.
Writes scores.json and scores.md into out_dir.
"""
import argparse
import json
import re
from collections import defaultdict
from pathlib import Path

from rapidfuzz import fuzz

LOOKALIKE = str.maketrans("abcehkmoptxy", "авсенкмортху")


def norm(s: str) -> str:
    s = s.lower().replace("ё", "е").translate(LOOKALIKE)
    return re.sub(r"[\W_]+", "", s)


def hit(fact: str, text: str) -> float:
    f, t = norm(fact), norm(text)
    if not f:
        return 0.0
    if f in t:
        return 1.0
    return 0.5 if fuzz.partial_ratio(f, t) >= 85 else 0.0


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("reference")
    ap.add_argument("out")
    args = ap.parse_args()

    ref = {k: v for k, v in json.loads(
        Path(args.reference).read_text(encoding="utf-8")).items()
        if not k.startswith("_")}
    engines = sorted(p.name for p in (Path(args.cache) / "out").iterdir() if p.is_dir())

    result = {}
    for eng in engines:
        d = Path(args.cache) / "out" / eng
        if not all((d / f"{pid}.txt").exists() for pid in ref):
            continue
        timings = json.loads((d / "timings.json").read_text()) if (d / "timings.json").exists() else {}
        costs = json.loads((d / "costs.json").read_text()) if (d / "costs.json").exists() else {}
        by_kind = defaultdict(lambda: [0.0, 0])
        pages = {}
        for pid, spec in ref.items():
            text = (d / f"{pid}.txt").read_text(encoding="utf-8")
            got = 0.0
            misses = []
            for kind, fact in spec["facts"]:
                h = hit(fact, text)
                got += h
                by_kind[kind][0] += h
                by_kind[kind][1] += 1
                if h < 1:
                    misses.append(fact)
            pages[pid] = {"score": round(got / len(spec["facts"]), 3),
                          "misses": misses, "seconds": timings.get(pid)}
        total = sum(v[0] for v in by_kind.values()) / sum(v[1] for v in by_kind.values())
        secs = [p["seconds"] for p in pages.values() if p["seconds"] is not None]
        result[eng] = {
            "total": round(total, 3),
            "by_kind": {k: round(v[0] / v[1], 3) for k, v in sorted(by_kind.items())},
            "sec_per_page": round(sum(secs) / len(secs), 1) if secs else None,
            "usd_per_page": round(sum(costs.get(p) or 0 for p in ref) / len(ref), 4),
            "pages": pages,
        }

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    (out / "scores.json").write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")

    kinds = sorted({k for r in result.values() for k in r["by_kind"]})
    lines = ["| Движок | Всего | " + " | ".join(kinds) + " | сек/стр | $/стр |",
             "|---|---|" + "---|" * len(kinds) + "---|---|"]
    for eng, r in sorted(result.items(), key=lambda kv: -kv[1]["total"]):
        lines.append(f"| {eng} | {r['total']:.0%} | "
                     + " | ".join(f"{r['by_kind'].get(k, 0):.0%}" for k in kinds)
                     + f" | {r['sec_per_page']} | {r['usd_per_page']} |")
    lines += ["", "| Страница | " + " | ".join(result) + " |",
              "|---|" + "---|" * len(result)]
    for pid, spec in ref.items():
        lines.append(f"| {pid} ({spec['layout']}) | "
                     + " | ".join(f"{result[e]['pages'][pid]['score']:.0%}" for e in result) + " |")
    (out / "scores.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
