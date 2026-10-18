"""Copy sample PDFs into a local cache and render every page to PNG.

Usage: python render.py <source_dir> <cache_dir> [--dpi 300]

Writes <cache_dir>/pdf/<sha256>.pdf, <cache_dir>/pages/<sha12>-pNN.png and
<cache_dir>/manifest.json (one entry per page with its source path).
"""
import argparse
import hashlib
import json
import shutil
from pathlib import Path

import pymupdf


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("source")
    ap.add_argument("cache")
    ap.add_argument("--dpi", type=int, default=300)
    args = ap.parse_args()

    src, cache = Path(args.source), Path(args.cache)
    (cache / "pdf").mkdir(parents=True, exist_ok=True)
    (cache / "pages").mkdir(parents=True, exist_ok=True)

    manifest = []
    for path in sorted(src.rglob("*.pdf")):
        data = path.read_bytes()
        sha = hashlib.sha256(data).hexdigest()
        local = cache / "pdf" / f"{sha}.pdf"
        if not local.exists():
            shutil.copyfile(path, local)
        doc = pymupdf.open(local)
        for i, page in enumerate(doc, start=1):
            out = cache / "pages" / f"{sha[:12]}-p{i:02d}.png"
            if not out.exists():
                page.get_pixmap(dpi=args.dpi, colorspace=pymupdf.csGRAY).save(out)
            manifest.append({
                "page_id": out.stem,
                "sha256": sha,
                "page": i,
                "pages": doc.page_count,
                "source": str(path.relative_to(src)).replace("\\", "/"),
                "image": out.name,
            })
        print(f"{path.relative_to(src)}: {doc.page_count} pages")
    (cache / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=1), encoding="utf-8")
    print(f"total pages: {len(manifest)}")


if __name__ == "__main__":
    main()
