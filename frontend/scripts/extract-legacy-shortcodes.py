#!/usr/bin/env python3
"""Extract every distinct legacy [youtube]/[soundcloud] block from a WordPress
export so the editor's round-trip test runs against the real corpus.

Usage: python3 scripts/extract-legacy-shortcodes.py /path/to/wp-posts.xml
Writes src/components/__fixtures__/legacy-shortcodes.json.
"""
import json
import re
import sys
from collections import Counter

SHORTCODE = re.compile(r"\[(youtube|soundcloud)\b[^\]]*\]", re.I)
BLOCK = re.compile(r"^<(p|div)(\s[^>]*)?>\s*(\[[^\]]*\])\s*</\1>$", re.I)
# The editor stores a wrapped block as the browser serializes it, which writes
# a bare & in text as &amp;. Bare blocks are never converted, so stay raw.
BARE_AMPERSAND = re.compile(r"&(?![A-Za-z]+;|#[0-9]+;|#x[0-9A-Fa-f]+;)")


def main(path: str) -> None:
    text = open(path, encoding="utf-8", errors="ignore").read()
    bodies = re.findall(r"<content:encoded><!\[CDATA\[(.*?)\]\]></content:encoded>", text, re.S)
    occurrences = Counter()
    blocks = {}
    inline = 0
    for body in bodies:
        for line in body.split("\n"):
            line = line.strip()
            if not SHORTCODE.search(line):
                continue
            m = BLOCK.match(line)
            if m:
                occurrences["block"] += 1
                normalized = BARE_AMPERSAND.sub("&amp;", line)
                blocks.setdefault(normalized, {"block": normalized, "wrapper": m.group(1).lower()})
            elif SHORTCODE.fullmatch(line):
                occurrences["bare"] += 1
                blocks.setdefault(line, {"block": line, "wrapper": "bare"})
            else:
                inline += 1
    out = {
        "source": "wp-posts.xml export, see docs/superpowers/specs/2026-10-06-article-embeds-design.md section 2",
        "occurrences": dict(occurrences),
        "inlineWithProse": inline,
        "distinct": sorted(blocks.values(), key=lambda b: b["block"]),
    }
    with open("src/components/__fixtures__/legacy-shortcodes.json", "w", encoding="utf-8") as f:
        json.dump(out, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(json.dumps({k: v for k, v in out.items() if k != "distinct"}), "distinct:", len(out["distinct"]))


if __name__ == "__main__":
    main(sys.argv[1])
