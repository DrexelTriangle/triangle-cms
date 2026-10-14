#!/usr/bin/env python3
"""Draft "Exhibit of the day" entries from a sourced inventory.

Reads scripts/exhibits/exhibits.json, asks Claude for a short write-up per
exhibit using only the supplied facts, and writes exhibit_spotlights.draft.json
in the CMS entry shape with every entry disabled. Editors review, set dates,
enable, and import with PATCH /v1/settings/exhibit-spotlights.

Uses the `claude -p` CLI unless ANTHROPIC_API_KEY is set, in which case it
calls the Messages API directly. Standard library only.
"""
import argparse
import json
import os
import subprocess
import sys
import urllib.request
from pathlib import Path
from typing import Dict, List, Optional

HERE = Path(__file__).resolve().parent
INVENTORY = HERE / "exhibits" / "exhibits.json"
DEFAULT_OUT = HERE / "exhibits" / "exhibit_spotlights.draft.json"
MODEL = "claude-sonnet-5-5"

PROMPT = """You write short museum spotlights for a student newspaper's homepage.
Use ONLY the facts below. Do not add dates, numbers, names, or claims that are not in them.
The museum closed to the public on September 30, 2026, so describe the exhibit in the past tense.
If a fact list is thin, write less. Plain prose, no headings, no bullet characters, no em dashes.
Sentence case. No puffery (avoid words like iconic, beloved, testament, pivotal).

Exhibit: {name}
Hall or type: {hall}
Tenure (may be empty; if empty, do not say how long it was at the museum): {tenure}
Facts:
{facts}

Return JSON only, with exactly these keys:
{{"body": "two or three short paragraphs separated by a blank line, 90 to 160 words total",
  "fun_facts": ["two to four one-sentence facts taken from the list above"]}}
"""


def build_prompt(exhibit: Dict) -> str:
    facts = "\n".join(f"- {f}" for f in exhibit.get("facts", []))
    return PROMPT.format(name=exhibit["name"], hall=exhibit.get("hall", ""),
                         tenure=exhibit.get("tenure", ""), facts=facts)


def ask_cli(prompt: str) -> str:
    print("  calling claude CLI", file=sys.stderr)
    result = subprocess.run(
        ["claude", "-p", prompt, "--output-format", "json", "--model", MODEL],
        capture_output=True, text=True, timeout=180, check=True,
    )
    return json.loads(result.stdout)["result"]


def ask_api(prompt: str, api_key: str) -> str:
    print("  calling Messages API", file=sys.stderr)
    body = json.dumps({"model": MODEL, "max_tokens": 800,
                       "messages": [{"role": "user", "content": prompt}]}).encode()
    req = urllib.request.Request(
        "https://api.anthropic.com/v1/messages", data=body, method="POST",
        headers={"x-api-key": api_key, "anthropic-version": "2023-06-01",
                 "content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        data = json.load(resp)
    return "".join(block.get("text", "") for block in data["content"])


def parse_reply(text: str) -> Optional[Dict]:
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end < 0:
        return None
    try:
        data = json.loads(text[start:end + 1])
    except json.JSONDecodeError:
        return None
    if not isinstance(data.get("body"), str) or not isinstance(data.get("fun_facts"), list):
        return None
    return data


def draft_one(exhibit: Dict, api_key: Optional[str]) -> Dict:
    prompt = build_prompt(exhibit)
    reply = None
    for attempt in range(2):
        text = ask_api(prompt, api_key) if api_key else ask_cli(prompt)
        reply = parse_reply(text)
        if reply:
            break
        print(f"  malformed reply, attempt {attempt + 1}", file=sys.stderr)
    if not reply:
        raise RuntimeError(f"no usable reply for {exhibit['slug']}")
    image = exhibit.get("image", {})
    return {
        "slug": exhibit["slug"],
        "feature_date": "",
        "enabled": False,
        "title": exhibit["name"],
        "kicker": exhibit.get("hall", ""),
        "body": reply["body"].strip(),
        "tenure": exhibit.get("tenure", ""),
        "fun_facts": [str(f).strip() for f in reply["fun_facts"] if str(f).strip()],
        "link_url": exhibit.get("link_url", ""),
        "image_url": "",
        "image_alt": image.get("alt", ""),
        "image_credit": image.get("credit", ""),
        "image_credit_url": image.get("commons_file", ""),
        "sources": exhibit.get("sources", []),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--only", help="draft one slug")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    args = parser.parse_args()

    inventory_all: List[Dict] = json.loads(INVENTORY.read_text())
    inventory = inventory_all
    if args.only:
        inventory = [e for e in inventory if e["slug"] == args.only]
        if not inventory:
            print(f"no exhibit with slug {args.only}", file=sys.stderr)
            return 1
    api_key = os.environ.get("ANTHROPIC_API_KEY")

    drafts = []
    for exhibit in inventory:
        print(f"drafting {exhibit['slug']}", file=sys.stderr)
        drafts.append(draft_one(exhibit, api_key))

    if args.only and args.out.exists():
        # Redrafting one slug must not throw away the other drafts or any edits
        # editors already made in the output file.
        existing = json.loads(args.out.read_text())
        order = {e["slug"]: i for i, e in enumerate(inventory_all)}
        drafts = [e for e in existing if e.get("slug") != args.only] + drafts
        drafts.sort(key=lambda e: order.get(e.get("slug"), len(order)))

    args.out.write_text(json.dumps(drafts, indent=2, ensure_ascii=False) + "\n")
    print(f"wrote {len(drafts)} draft(s) to {args.out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
