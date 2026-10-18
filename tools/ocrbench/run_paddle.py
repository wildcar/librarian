"""Run PaddleOCR (PP-OCRv5, Cyrillic) on rendered pages.

Usage: python run_paddle.py <cache_dir> [--only page_id,...]

Writes <cache_dir>/out/paddle/<page_id>.txt and timings.json.
"""
import argparse
import json
import time
from pathlib import Path

import torch  # noqa: F401  # must load before paddle on Windows (DLL clash)
from paddleocr import PaddleOCR

from lines import to_text


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache")
    ap.add_argument("--only", default="")
    args = ap.parse_args()

    cache = Path(args.cache)
    out = cache / "out" / "paddle"
    out.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
    only = set(filter(None, args.only.split(",")))
    todo = [e for e in manifest
            if (not only or e["page_id"] in only)
            and not (out / f"{e['page_id']}.txt").exists()]

    ocr = PaddleOCR(
        lang="ru",
        text_detection_model_name="PP-OCRv5_mobile_det",
        text_recognition_model_name="eslav_PP-OCRv5_mobile_rec",
        use_doc_orientation_classify=True,
        use_doc_unwarping=False,
        use_textline_orientation=True,
        # oneDNN path fails on Windows CPU in paddle 3.3 (pir::ArrayAttribute).
        enable_mkldnn=False,
    )
    tf = out / "timings.json"
    timings = json.loads(tf.read_text()) if tf.exists() else {}
    for e in todo:
        t0 = time.perf_counter()
        res = ocr.predict(str(cache / "pages" / e["image"]))[0]
        items = [(t, tuple(map(float, b)))
                 for t, b in zip(res["rec_texts"], res["rec_boxes"])]
        (out / f"{e['page_id']}.txt").write_text(to_text(items), encoding="utf-8")
        timings[e["page_id"]] = round(time.perf_counter() - t0, 2)
        print(e["page_id"], timings[e["page_id"]], flush=True)
        tf.write_text(json.dumps(timings, indent=1))


if __name__ == "__main__":
    main()
