"""Run tesseract on every rendered page.

Usage: python run_tesseract.py <cache_dir> [--workers 4] [--only page_id,...]

Writes <cache_dir>/out/tesseract/<page_id>.txt and timings.json.
"""
import argparse
import json
import os
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

EXE = os.environ.get(
    "TESSERACT_EXE",
    str(Path.home() / "AppData/Local/Programs/Tesseract-OCR/tesseract.exe"))
TESSDATA = os.environ.get(
    "TESSDATA_DIR", str(Path.home() / "AppData/Local/librarian/tessdata"))


def ocr(image: Path, out_base: Path) -> float:
    t0 = time.perf_counter()
    # psm 1: automatic page segmentation with orientation detection.
    subprocess.run(
        [EXE, str(image), str(out_base), "--tessdata-dir", TESSDATA,
         "-l", "rus+eng", "--psm", "1"],
        check=True, capture_output=True)
    return time.perf_counter() - t0


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--only", default="")
    args = ap.parse_args()

    cache = Path(args.cache)
    out = cache / "out" / "tesseract"
    out.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
    only = set(filter(None, args.only.split(",")))
    todo = [e for e in manifest
            if (not only or e["page_id"] in only)
            and not (out / f"{e['page_id']}.txt").exists()]

    timings = {}
    with ThreadPoolExecutor(args.workers) as pool:
        futures = {e["page_id"]: pool.submit(
            ocr, cache / "pages" / e["image"], out / e["page_id"]) for e in todo}
        for pid, f in futures.items():
            timings[pid] = round(f.result(), 2)
            print(pid, timings[pid], flush=True)

    tf = out / "timings.json"
    old = json.loads(tf.read_text()) if tf.exists() else {}
    old.update(timings)
    tf.write_text(json.dumps(old, indent=1))


if __name__ == "__main__":
    main()
