#!/usr/bin/env python3
"""Probe catalog mirrors. Report only. Never edits sites.json.

Usage:
  python3 internal/indexer/catalog/check_mirrors.py
  python3 internal/indexer/catalog/check_mirrors.py --workers 40 --timeout 8
"""

from __future__ import annotations

import argparse
import json
import ssl
import sys
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parent
SITES = ROOT / "sites.json"
PRESETS = ROOT / "presets.go"

# Hosts that answer are not missing mirrors. 401/403 means the name exists.
OK_STATUSES = set(range(200, 400)) | {401, 403, 405, 429}
BROKEN_STATUSES = {404, 410, 451}


@dataclass(frozen=True)
class Target:
    site_id: str
    name: str
    url: str
    source: str


@dataclass
class Result:
    target: Target
    kind: str
    detail: str


def load_targets() -> list[Target]:
    sites = json.loads(SITES.read_text(encoding="utf-8"))
    seen: set[tuple[str, str]] = set()
    out: list[Target] = []
    for site in sites:
        site_id = site.get("id") or ""
        name = site.get("name") or site_id
        urls = list(site.get("urls") or [])
        info = (site.get("info_url") or "").strip()
        if info and info not in urls:
            urls.insert(0, info)
        for raw in urls:
            url = raw.strip()
            if not url:
                continue
            key = (site_id, url)
            if key in seen:
                continue
            seen.add(key)
            out.append(Target(site_id, name, url, "sites.json"))
    if PRESETS.exists():
        import re

        text = PRESETS.read_text(encoding="utf-8")
        current_id = ""
        current_name = ""
        for line in text.splitlines():
            if m := re.search(r'ID:\s+"([^"]+)"', line):
                current_id = m.group(1)
            elif m := re.search(r'Name:\s+"([^"]+)"', line):
                current_name = m.group(1)
            elif m := re.search(r'(?:URL|InfoURL):\s+"(https?://[^"]+)"', line):
                url = m.group(1)
                key = (current_id, url)
                if key in seen:
                    continue
                seen.add(key)
                out.append(Target(current_id, current_name or current_id, url, "presets.go"))
    return out


def classify_url(url: str) -> str | None:
    parsed = urlparse(url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        return "invalid URL"
    if parsed.query or parsed.fragment:
        return "not a site root (query/fragment leftover)"
    path = parsed.path.rstrip("/")
    if path and path.lower().endswith((".php", ".html", ".aspx", ".htm")):
        return "not a site root (page path leftover)"
    return None


def probe(url: str, timeout: float) -> tuple[str, str]:
    ctx = ssl.create_default_context()
    req = urllib.request.Request(
        url,
        method="HEAD",
        headers={"User-Agent": "Caravan-catalog-mirror-check/1.0"},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
            status = getattr(resp, "status", 200)
            return classify_status(status)
    except urllib.error.HTTPError as err:
        # Some hosts refuse HEAD. GET the first byte of the body instead.
        if err.code in {405, 501}:
            return probe_get(url, timeout, ctx)
        return classify_status(err.code)
    except Exception as err:
        if "HEAD" in str(err) or "Method Not Allowed" in str(err):
            return probe_get(url, timeout, ctx)
        return "dead", short_error(err)


def probe_get(url: str, timeout: float, ctx: ssl.SSLContext) -> tuple[str, str]:
    req = urllib.request.Request(
        url,
        method="GET",
        headers={"User-Agent": "Caravan-catalog-mirror-check/1.0", "Range": "bytes=0-0"},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
            return classify_status(getattr(resp, "status", 200))
    except urllib.error.HTTPError as err:
        return classify_status(err.code)
    except Exception as err:
        return "dead", short_error(err)


def classify_status(status: int) -> tuple[str, str]:
    if status in OK_STATUSES:
        return "ok", f"HTTP {status}"
    if status in BROKEN_STATUSES or 500 <= status <= 599:
        return "http", f"HTTP {status}"
    return "http", f"HTTP {status}"


def short_error(err: Exception) -> str:
    text = str(err).strip() or err.__class__.__name__
    text = text.replace("\n", " ")
    if len(text) > 160:
        text = text[:157] + "..."
    return text


def check_one(target: Target, timeout: float) -> Result:
    leftover = classify_url(target.url)
    if leftover:
        return Result(target, "leftover", leftover)
    kind, detail = probe(target.url, timeout)
    return Result(target, kind, detail)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workers", type=int, default=32)
    parser.add_argument("--timeout", type=float, default=8.0)
    parser.add_argument("--json", action="store_true", help="print broken rows as JSON")
    args = parser.parse_args()

    targets = load_targets()
    print(f"checking {len(targets)} URLs with {args.workers} workers, {args.timeout:.0f}s timeout", file=sys.stderr)

    results: list[Result] = []
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = [pool.submit(check_one, target, args.timeout) for target in targets]
        done = 0
        for future in as_completed(futures):
            results.append(future.result())
            done += 1
            if done % 100 == 0 or done == len(targets):
                print(f"  {done}/{len(targets)}", file=sys.stderr)

    results.sort(key=lambda row: (row.kind, row.target.name.lower(), row.target.url))
    broken = [row for row in results if row.kind != "ok"]
    counts: dict[str, int] = {}
    for row in results:
        counts[row.kind] = counts.get(row.kind, 0) + 1

    if args.json:
        print(json.dumps([
            {
                "id": row.target.site_id,
                "name": row.target.name,
                "url": row.target.url,
                "source": row.target.source,
                "kind": row.kind,
                "detail": row.detail,
            }
            for row in broken
        ], indent=2))
    else:
        print(f"ok {counts.get('ok', 0)}  dead {counts.get('dead', 0)}  "
              f"http {counts.get('http', 0)}  leftover {counts.get('leftover', 0)}")
        if not broken:
            print("all catalog mirrors answered")
            return 0
        print()
        print("BROKEN")
        for row in broken:
            print(f"{row.kind:8}  {row.target.site_id:24}  {row.target.url}  {row.detail}")
    return 0 if not any(row.kind == "dead" for row in broken) else 1


if __name__ == "__main__":
    raise SystemExit(main())
