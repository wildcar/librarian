# ocrbench

OCR bake-off on real scans: renders pages, runs several engines, scores them
against key facts read by eye. Results and the reference live outside the repo
(they contain personal data), in the archive's `_эксперименты/` folder.

```bash
py -3.14 -m venv .venv && .venv/Scripts/pip install pymupdf pillow rapidfuzz mcp uv
.venv/Scripts/uv venv -p 3.12 .venv312 && .venv/Scripts/uv pip install -p .venv312 surya-ocr paddlepaddle paddleocr

C=$LOCALAPPDATA/librarian/ocrbench
.venv/Scripts/python render.py "<source folder>" $C            # pages → PNG, manifest
.venv/Scripts/python run_tesseract.py $C                        # needs tesseract + rus tessdata_best
.venv312/Scripts/python run_paddle.py $C --only <ids>
LLAMA_CPP_BINARY=<llama-server.exe> .venv312/Scripts/python run_surya.py $C --only <ids>
.venv/Scripts/python run_router.py $C --models <id1,id2> --only <ids>   # ROUTER_URL/ROUTER_TOKEN in ../../.env
.venv/Scripts/python score.py $C <reference.json> <out dir>
.venv/Scripts/python split.py $C <split_reference.json> <out dir>      # document boundaries: rules, decide, hybrid, chat
```

Scoring: a fact scores 1 when found verbatim after normalisation (case, ё,
punctuation, Latin/Cyrillic look-alikes), 0.5 on a fuzzy match (≥ 85), else 0.
