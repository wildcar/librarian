"""Transcribe rendered pages with vision models through model-router-mcp.

Usage:
  python run_router.py --list
  python run_router.py <cache_dir> --models id1,id2 [--only page_id,...]

Reads ROUTER_URL (the /mcp endpoint) and ROUTER_TOKEN from the environment or
from the repository's .env. Writes <cache_dir>/out/router-<model>/<page_id>.txt,
timings.json and costs.json.
"""
import argparse
import asyncio
import base64
import io
import json
import os
import re
import time
from pathlib import Path

from mcp import ClientSession
from mcp.client.streamable_http import create_mcp_http_client, streamable_http_client
from PIL import Image

PROMPT = """Перепиши весь текст с этой страницы скана дословно, на языке оригинала.
Правила:
- Сохраняй опечатки и сокращения как есть, ничего не исправляй и не дописывай.
- Таблицы: каждая строка таблицы на одной строке, ячейки через " | ".
- Рукописный текст переписывай тоже; нечитаемое слово помечай [?].
- Текст штампов и печатей переписывай отдельной строкой с пометкой [штамп].
- Если страница повёрнута, читай её в правильной ориентации.
- Графики, ЭКГ, снимки УЗИ не описывай, только подписи и цифры на них.
Выведи только текст страницы, без комментариев."""


def load_env() -> None:
    env = Path(__file__).resolve().parents[2] / ".env"
    if env.exists():
        for line in env.read_text(encoding="utf-8").splitlines():
            m = re.match(r"\s*([A-Z_]+)\s*=\s*(.*)\s*$", line)
            if m and m.group(1) not in os.environ:
                os.environ[m.group(1)] = m.group(2).strip().strip('"')


def page_jpeg(path: Path, long_side: int = 2000) -> str:
    im = Image.open(path).convert("L")
    im.thumbnail((long_side, long_side))
    buf = io.BytesIO()
    im.save(buf, "JPEG", quality=88)
    return base64.b64encode(buf.getvalue()).decode()


def payload(result) -> dict:
    if result.structured_content:
        sc = result.structured_content
        return sc.get("result", sc) if isinstance(sc, dict) else {"result": sc}
    return json.loads(result.content[0].text)


async def run(args) -> None:
    headers = {"Authorization": f"Bearer {os.environ['ROUTER_TOKEN']}"}
    http = create_mcp_http_client(headers=headers, timeout=None)
    async with http, streamable_http_client(os.environ["ROUTER_URL"], http_client=http) as streams:
        async with ClientSession(streams[0], streams[1]) as s:
            await s.initialize()
            if args.list:
                res = await s.call_tool("list_models", {"task": "image-to-text"})
                for m in payload(res) if isinstance(payload(res), list) else payload(res)["result"]:
                    print(m.get("model_id") or m.get("id"), m.get("tier"),
                          m.get("input_price_per_mtok") or m.get("price_input"),
                          m.get("output_price_per_mtok") or m.get("price_output"))
                return
            cache = Path(args.cache)
            manifest = json.loads((cache / "manifest.json").read_text(encoding="utf-8"))
            only = set(filter(None, args.only.split(",")))
            for model in filter(None, args.models.split(",")):
                out = cache / "out" / ("router-" + re.sub(r"[^\w.-]+", "_", model))
                out.mkdir(parents=True, exist_ok=True)
                tf, cf = out / "timings.json", out / "costs.json"
                timings = json.loads(tf.read_text()) if tf.exists() else {}
                costs = json.loads(cf.read_text()) if cf.exists() else {}
                for e in manifest:
                    pid = e["page_id"]
                    if (only and pid not in only) or (out / f"{pid}.txt").exists():
                        continue
                    t0 = time.perf_counter()
                    res = await s.call_tool("chat", {
                        "external_user_id": "librarian",
                        "app_name": "librarian",
                        "model_id": model,
                        "text": PROMPT,
                        "images_b64": [page_jpeg(cache / "pages" / e["image"])],
                        "image_mime": "image/jpeg",
                        "params": {"temperature": 0},
                    })
                    if res.is_error:
                        print(model, pid, "ERROR", res.content[0].text[:300], flush=True)
                        continue
                    p = payload(res)
                    (out / f"{pid}.txt").write_text(p.get("text") or "", encoding="utf-8")
                    timings[pid] = round(time.perf_counter() - t0, 2)
                    costs[pid] = p.get("cost") or p.get("cost_usd") or p.get("usage", {}).get("cost")
                    print(model, pid, timings[pid], costs[pid], flush=True)
                    tf.write_text(json.dumps(timings, indent=1))
                    cf.write_text(json.dumps(costs, indent=1))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cache", nargs="?")
    ap.add_argument("--models", default="")
    ap.add_argument("--only", default="")
    ap.add_argument("--list", action="store_true")
    args = ap.parse_args()
    load_env()
    asyncio.run(run(args))


if __name__ == "__main__":
    main()
