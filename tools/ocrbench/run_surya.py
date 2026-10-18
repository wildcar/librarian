"""Run surya OCR (VLM through llama.cpp on CPU) on rendered pages.

Usage: python run_surya.py <cache_dir> [--only page_id,...]
Needs LLAMA_CPP_BINARY pointing at llama-server.exe.

Writes <cache_dir>/out/surya/<page_id>.txt and timings.json.
"""
import argparse
import html
import json
import re
import time
from pathlib import Path

from PIL import Image
from surya.recognition import RecognitionPredictor


def html_to_text(s: str) -> str:
    s = re.sub(r"</t[dh]>", " ", s)
    s = re.sub(r"</(tr|p|div|li|h\d)>|<br\s*/?>", "\n", s)
    s = re.sub(r"<[^>]+>", "", s)
    return re.sub(r"[ \t]+", " ", html.unescape(s)).strip()


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("--only", default="")
    args = ap.parse_args()

    cache = Path(args.cache)
    out = cache / "out" / "surya"
    out.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
    only = set(filter(None, args.only.split(",")))
    todo = [e for e in manifest
            if (not only or e["page_id"] in only)
            and not (out / f"{e['page_id']}.txt").exists()]

    rec = RecognitionPredictor()
    tf = out / "timings.json"
    timings = json.loads(tf.read_text()) if tf.exists() else {}
    for e in todo:
        img = Image.open(cache / "pages" / e["image"]).convert("RGB")
        t0 = time.perf_counter()
        page = rec([img], full_page=True)[0]
        blocks = sorted(page.blocks, key=lambda b: b.reading_order)
        text = "\n".join(html_to_text(b.html) for b in blocks if b.html)
        errors = sum(1 for b in blocks if b.error)
        if not text or errors:
            print(e["page_id"], f"blocks={len(blocks)} errors={errors}", flush=True)
        if not text:
            continue  # leave no file so a rerun retries the page
        (out / f"{e['page_id']}.txt").write_text(text, encoding="utf-8")
        timings[e["page_id"]] = round(time.perf_counter() - t0, 2)
        print(e["page_id"], timings[e["page_id"]], flush=True)
        tf.write_text(json.dumps(timings, indent=1))


if __name__ == "__main__":
    main()
