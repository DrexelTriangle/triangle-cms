#!/usr/bin/env python3
"""File every article that sits in a subsection under its parent section too.

The editor now adds the parent automatically when a subsection is checked, but
rows written before that (and everything the ETL imported) carry the subsection
alone. Those articles are missing from their parent section's own page: listing
by section expands to the subsections underneath it, so a reader browsing
"Special Editions" does see them -- but anything reading `articles.categories`
directly, including the public site's per-article section labels, sees only
"Welcome Week" with no parent above it.

  python ./scripts/backfill_parent_sections.py \
      --cms-dsn 'triangle_user@tcp(10.248.40.183:4006)/triangle' --skip-ssl --dry-run

The password comes from CMS_DB_PASSWORD (or a prompt), never from argv.
Re-runnable and additive: it only ever appends a parent that is missing, and
never removes or reorders what is already there, so a second run is a no-op.
Run it with --dry-run first and read the sample -- it rewrites a text column.

"Missing" is judged with the read path's fuzzy substring rule, not exact slug
equality: an article tagged "Arts & Entertainment" is already matched by the
"entertainment" section, so it does not also get "Entertainment" bolted on.
Skipping that distinction would have rewritten 2159 rows instead of 638 and put
a redundant second section label on 2000+ articles.

First run against DB1 on 2026-08-02: 638 of 10058 rows rewritten.

Archived (trashed) articles are included: they keep their sections while in the
trash and would otherwise come back restored-but-unparented.
"""

from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import NamedTuple

DSN_RE = re.compile(
    r"^(?P<user>[^:@/]+)(?::(?P<password>[^@]*))?@tcp\((?P<host>[^:)]+)(?::(?P<port>\d+))?\)/(?P<database>[^?]+)"
)

# Mirrors slugifyCategory in frontend/src/pages/editArticleView.tsx. Matching on
# the slugified title is an exact comparison, deliberately stricter than the
# fuzzy LIKE the read path uses: this writes to the database, so a near-miss
# must skip rather than guess a parent.
SLUG_SUB = re.compile(r"[^a-z0-9]+")


class Fail(Exception):
    """A preflight or verification failure with a human-readable reason."""


class Parent(NamedTuple):
    """A parent section: the title written into an article, and the slug the
    section's own listing filter matches on."""

    title: str
    slug: str


def info(msg: str) -> None:
    print(f"  {msg}")


def step(msg: str) -> None:
    print(f"\n==> {msg}")


def warn(msg: str) -> None:
    # stdout is block-buffered when piped; flush so warnings land in sequence
    # with the surrounding progress output instead of ahead of all of it.
    sys.stdout.flush()
    print(f"  WARNING: {msg}", file=sys.stderr)
    sys.stderr.flush()


def slugify(value: str) -> str:
    return SLUG_SUB.sub("-", value.strip().lower()).strip("-")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Add each article's parent section alongside any subsection it is filed under."
    )
    parser.add_argument(
        "--cms-dsn",
        required=True,
        help="CMS database: user:password@tcp(host:port)/database (password optional)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="report what would change without writing anything",
    )
    parser.add_argument(
        "--limit-sample",
        type=int,
        default=10,
        help="how many example rewrites to print (default: 10)",
    )
    parser.add_argument(
        "--skip-ssl",
        action="store_true",
        help=(
            "connect without TLS. Needed via the MaxScale proxy (10.248.40.183:4006), "
            "which does not offer TLS while MariaDB clients 11.4+ require it by default; "
            "the backend reaches it the same way over the internal LAN."
        ),
    )
    return parser.parse_args()


def resolve_dsn(dsn: str, label: str, password_env: str) -> dict:
    match = DSN_RE.match(dsn.strip())
    if not match:
        raise Fail(f"--{label}-dsn must look like user:password@tcp(host:port)/database")

    parts = match.groupdict()
    target = {
        "user": parts["user"],
        "host": parts["host"],
        "port": int(parts["port"] or 3306),
        "database": parts["database"],
        "password": parts["password"] or os.getenv(password_env) or "",
    }
    if not target["password"]:
        if not sys.stdin.isatty():
            raise Fail(f"no password for {label}: set {password_env}, or run interactively.")
        target["password"] = getpass.getpass(f"Password for {target['user']}@{target['host']} ({label}): ")
    return target


def mysql_client() -> str:
    for candidate in ("mariadb", "mysql"):
        if shutil.which(candidate):
            return candidate
    raise Fail("neither `mariadb` nor `mysql` is on PATH; install a MariaDB client.")


class Client:
    """Runs SQL against one target without ever putting the password in argv."""

    def __init__(self, target: dict, skip_ssl: bool = False):
        self.target = target
        self.binary = mysql_client()
        # An option file keeps the password out of `ps` and shell history. 0600,
        # in a private temp dir, removed on exit.
        self._dir = tempfile.mkdtemp(prefix="triangle-sections-")
        self.defaults = Path(self._dir) / "my.cnf"
        self.defaults.write_text(
            "[client]\n"
            f"host={target['host']}\n"
            f"port={target['port']}\n"
            f"user={target['user']}\n"
            f"password={target['password']}\n"
            + ("ssl=0\n" if skip_ssl else ""),
            encoding="utf-8",
        )
        self.defaults.chmod(0o600)

    def cleanup(self) -> None:
        shutil.rmtree(self._dir, ignore_errors=True)

    def rows(self, sql: str) -> list[list[str]]:
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"], "-N", "-B", "-e", sql]
        done = subprocess.run(cmd, capture_output=True, text=True)
        if done.returncode != 0:
            raise Fail(f"query failed on {self.target['database']}: {done.stderr.strip()}")
        return [line.split("\t") for line in done.stdout.splitlines() if line]

    def scalar(self, sql: str) -> str:
        rows = self.rows(sql)
        return rows[0][0] if rows and rows[0] else ""

    def execute_script(self, statements: list[str]) -> None:
        """Run many statements in one transaction, fed over stdin so the payload
        never has to fit in an argv-sized -e argument."""
        script = Path(self._dir) / "backfill.sql"
        script.write_text(
            "START TRANSACTION;\n" + "\n".join(statements) + "\nCOMMIT;\n",
            encoding="utf-8",
        )
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"]]
        with script.open("rb") as handle:
            done = subprocess.run(cmd, stdin=handle, capture_output=True, text=True)
        if done.returncode != 0:
            raise Fail(f"backfill failed on {self.target['database']}: {done.stderr.strip()}")


def quote(value: str) -> str:
    """Single-quoted SQL literal. These strings come from the database, so they
    get escaped rather than trusted."""
    return "'" + value.replace("\\", "\\\\").replace("'", "''") + "'"


class Unparseable(Exception):
    """Categories text that looks like JSON but is not, so it must not be rewritten."""


def decode_categories(raw: str) -> tuple[list[str], bool]:
    """The stored list plus whether it was JSON.

    Matches ParseStringListField on the Go side: a JSON array when it parses as
    one, otherwise a bare comma-separated list left over from the import.

    A value that opens with "[" but will not parse is neither. Go's reader
    comma-splits it, which is merely wrong in memory; appending to it here would
    write "[...],Parent" back to the column and make the damage permanent. Those
    rows are refused instead.
    """
    text = raw.strip()
    if not text:
        return [], True
    try:
        parsed = json.loads(text)
        if isinstance(parsed, list):
            return [str(item) for item in parsed], True
        raise Unparseable(text)
    except json.JSONDecodeError:
        if text.startswith("["):
            raise Unparseable(text) from None
    return [part for part in text.split(",")], False


def encode_categories(values: list[str], as_json: bool) -> str:
    """Serialize the way the read path and the section-matching LIKE both want.

    Note this writes a literal "&", where Go's json.Marshal (FormatTags) would
    emit "\\u0026". The category LIKE patterns are built from the plain
    character, so the escaped form silently fails to match -- keep the plain one.
    """
    if not as_json:
        return ",".join(values)
    return json.dumps(values, ensure_ascii=False)


def load_parent_titles(cms: Client) -> dict[str, Parent]:
    """Lookup key for a subsection -> canonical title of its parent section.

    Keyed on both the taxonomy slug and the slugified canonical title, because
    the two disagree wherever a title has an apostrophe: "Men's Basketball"
    slugifies to "men-s-basketball" while its stored slug is "mens-basketball".
    Articles store titles, so the title-derived key is the one that usually hits.
    """
    subsections = cms.rows(
        "SELECT `slug`, `canonical_title`, `parent_slug` FROM `site_taxonomy` "
        "WHERE `kind` = 'subsection' AND `parent_slug` IS NOT NULL AND `parent_slug` <> ''"
    )
    sections = cms.rows("SELECT `slug`, `canonical_title` FROM `site_taxonomy` WHERE `kind` = 'section'")
    title_by_slug = {row[0]: row[1] for row in sections if len(row) >= 2}

    parents: dict[str, Parent] = {}
    orphans: list[str] = []
    for row in subsections:
        if len(row) < 3:
            continue
        slug, canonical_title, parent_slug = row[0], row[1], row[2]
        title = title_by_slug.get(parent_slug)
        if not title:
            orphans.append(f"{slug} -> {parent_slug}")
            continue
        for key in (slug, slugify(canonical_title)):
            if key:
                parents[key] = Parent(title=title, slug=parent_slug)

    if orphans:
        warn(f"{len(orphans)} subsection(s) point at a section that does not exist; skipping them:")
        for entry in orphans[:10]:
            info(f"  {entry}")
    return parents


def category_match_patterns(slug: str) -> list[str]:
    """Mirrors CategoryMatchPatterns in server/internal/database/taxonomy.go.

    The substrings a section's LIKE filter matches on, minus the SQL wildcards.
    """
    normalized = slug.strip().lower()
    if not normalized:
        return []

    patterns = [normalized]
    if "-" in normalized:
        spaced = normalized.replace("-", " ")
        for variant in (spaced, normalized.replace("-", " & ")):
            if variant not in patterns:
                patterns.append(variant)
    return patterns


def parent_already_covered(values: list[str], parent: Parent) -> bool:
    """Whether the article is already filed under this parent section.

    Deliberately uses the read path's fuzzy substring rule rather than exact
    slug equality. The legacy category "Arts & Entertainment" is not slug-equal
    to the section title "Entertainment", but `%entertainment%` matches it, so
    those articles already list under the section -- adding the canonical title
    as well would only paint a second, redundant section label on the article.
    """
    patterns = category_match_patterns(parent.slug)
    for value in values:
        lowered = value.strip().lower()
        if slugify(value) == slugify(parent.title):
            return True
        if any(pattern in lowered for pattern in patterns):
            return True
    return False


def plan_row(raw: str, parents: dict[str, Parent]) -> str | None:
    """The rewritten categories value for one row, or None to leave it alone."""
    # -N -B renders a SQL NULL as the literal \N; nothing to do with it.
    if raw == "\\N":
        return None

    values, as_json = decode_categories(raw)  # may raise Unparseable
    additions: list[str] = []
    for value in values:
        parent = parents.get(slugify(value))
        if not parent:
            continue
        # Checked against the running list so a second subsection under the same
        # parent does not add it twice.
        if parent_already_covered(values + additions, parent):
            continue
        additions.append(parent.title)

    if not additions:
        return None
    return encode_categories(values + additions, as_json)


def main() -> int:
    args = parse_args()
    cms_client: Client | None = None

    try:
        cms_client = Client(resolve_dsn(args.cms_dsn, "cms", "CMS_DB_PASSWORD"), skip_ssl=args.skip_ssl)

        step("Reading the section tree")
        parent_titles = load_parent_titles(cms_client)
        if not parent_titles:
            raise Fail(
                "no subsections with a valid parent in site_taxonomy; "
                "nothing to inherit. Check this is the CMS database."
            )
        info(f"{len(parent_titles)} subsection(s) with a parent section")

        step("Scanning articles")
        rows = cms_client.rows(
            "SELECT `id`, `categories` FROM `articles` "
            "WHERE `categories` IS NOT NULL AND TRIM(`categories`) <> ''"
        )
        info(f"{len(rows)} article(s) carry categories")

        updates: list[str] = []
        samples: list[str] = []
        unparseable: list[str] = []
        for row in rows:
            if len(row) < 2:
                continue
            article_id, raw = row[0], row[1]
            try:
                encoded = plan_row(raw, parent_titles)
            except Unparseable:
                unparseable.append(str(article_id))
                continue
            if encoded is None:
                continue

            updates.append(f"UPDATE `articles` SET `categories` = {quote(encoded)} WHERE `id` = {int(article_id)};")
            if len(samples) < args.limit_sample:
                samples.append(f"#{article_id}: {raw}  ->  {encoded}")

        if unparseable:
            warn(
                f"{len(unparseable)} article(s) have categories that start with '[' but are not valid "
                "JSON; left untouched rather than risk corrupting them:"
            )
            info(f"  ids: {', '.join(unparseable[:20])}" + (" ..." if len(unparseable) > 20 else ""))

        step("Applying")
        info(f"{len(updates)} article(s) need a parent section added")
        for sample in samples:
            info(f"  {sample}")
        if len(updates) > len(samples):
            info(f"  ... and {len(updates) - len(samples)} more")

        if not updates:
            info("Nothing to do.")
            return 0
        if args.dry_run:
            info("--dry-run: nothing written.")
            return 0

        cms_client.execute_script(updates)
        info(f"{len(updates)} article(s) updated.")

        # No count rebuild needed: site_taxonomy.article_count buckets by section
        # plus its subsections, so these articles were already counted under the
        # parent. Confirmed on DB1 2026-08-02 -- comics-puzzles read 331 both
        # before and after. This changes which sections an article names, not
        # any total.
        info("Section totals are unaffected; no taxonomy count rebuild needed.")
        return 0

    except Fail as err:
        print(f"\nERROR: {err}", file=sys.stderr)
        return 1
    finally:
        if cms_client:
            cms_client.cleanup()


if __name__ == "__main__":
    sys.exit(main())
