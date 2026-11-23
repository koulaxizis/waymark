#!/usr/bin/env python3
"""Write the Waymark download count into index.html.

The count comes from the public numbers of the Koulaxizis Apps download
counter (https://apps.koulaxizis.workers.dev/stats.json, kept in the orOS
repository, tools/apps-counter/): every channel of every site.waymarked.*
app. The page used to fetch it in the visitor's browser; it is now written
here, once a day, so the page sends no request to anyone but its own host
(compliance audit 2026-11-23, proposal 4).

The line stays hidden until there are more than 10 downloads (Chris
2026-11-21). The script changes index.html only when the number changes,
and leaves it alone when the counter cannot be read.

Usage: python3 tools/update_downloads.py [path/to/index.html]
"""

import json
import re
import sys
import urllib.request

URL = "https://apps.koulaxizis.workers.dev/stats.json"
PREFIX = "site.waymarked."
MIN = 11


def fetch_total(url=URL):
    req = urllib.request.Request(url, headers={"User-Agent": "waymarked.site build"})
    with urllib.request.urlopen(req, timeout=30) as r:
        stats = json.load(r)
    if not isinstance(stats, dict) or stats.get("v") != 1 or not isinstance(stats.get("apps"), dict):
        raise ValueError("unexpected stats.json format")
    total = 0
    for pkg, row in stats["apps"].items():
        if not pkg.startswith(PREFIX) or not isinstance(row, dict):
            continue
        for v in row.values():
            if isinstance(v, (int, float)):
                total += int(v)
            elif isinstance(v, dict) and isinstance(v.get("n"), (int, float)):
                total += int(v["n"])
    return total


def fmt(n, lang):
    s = f"{n:,}"
    return s.replace(",", ".") if lang == "el" else s


# The line in index.html: <p ... id="wm-downloads" ...>, with one <b data-n>
# inside each language span.
LINE = re.compile(r'(<p[^>]*\bid="wm-downloads"[^>]*>)(.*?)(</p>)', re.S)
NUM = re.compile(r'(<span lang="(en|el)"><b data-n>)[^<]*(</b>)')


def render(html, n):
    m = LINE.search(html)
    if not m:
        raise ValueError('no id="wm-downloads" line in the page')
    open_tag, body, close = m.groups()
    show = n >= MIN
    open_tag = re.sub(r"\s+hidden\b", "", open_tag)
    if not show:
        open_tag = open_tag[:-1] + " hidden>"
    body = NUM.sub(lambda b: b.group(1) + (fmt(n, b.group(2)) if show else "") + b.group(3), body)
    return html[: m.start()] + open_tag + body + close + html[m.end():]


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "index.html"
    try:
        n = fetch_total()
    except Exception as e:  # counter down: keep the last number
        print(f"counter not read, page unchanged: {e}")
        return 0
    with open(path, encoding="utf-8") as f:
        html = f.read()
    new = render(html, n)
    if new != html:
        with open(path, "w", encoding="utf-8") as f:
            f.write(new)
        print(f"downloads: {n} (page updated)")
    else:
        print(f"downloads: {n} (no change)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
