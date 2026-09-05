#!/usr/bin/env python3
"""List the articles that share a slug, and optionally give each one its own.

Two articles can carry the same slug: `articles`.`slug` has a prefix index and
no UNIQUE constraint (see EnsureArticlesSlugIndex for why), and until the
id-qualified editor routes landed, creating an article whose title matched an
existing one filed a second row on the same slug. The CMS then addressed
articles by slug alone, so opening either one loaded the first match, and the
newer article "became" the older one.

  python ./scripts/report_duplicate_slugs.py \
      --cms-dsn 'triangle_user@tcp(10.248.40.183:4006)/triangle' --skip-ssl

The password comes from CMS_DB_PASSWORD (or a prompt), never from argv.
Read-only by default: it prints the groups and exits. Pass --apply to rename
the shadowed rows.

WHICH ROW KEEPS THE BARE SLUG. The one the site serves today: the lowest id
among the non-archived rows, or the lowest id overall if every row in the group
is archived. That is exactly what GetArticle resolves a duplicated slug to, so
the repair never moves a URL a reader or Google already has; it only gives a
working permalink to the rows that had none. The others are renamed with the
same numeric suffix the create path would have picked, first free wins.

Re-runnable: a second run finds nothing, because the first left one row per
slug. Archived rows are included: they keep their slug in the trash and would
otherwise collide again the moment someone restores one.
"""

from __future__ import annotations

import argparse
import getpass
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

DSN_RE = re.compile(
    r"^(?P<user>[^:@/]+)(?::(?P<password>[^@]*))?@tcp\((?P<host>[^:)]+)(?::(?P<port>\d+))?\)/(?P<database>[^?]+)"
)

# The server stops at -100 (see reserveArticleSlug); a repair that searched
# further would hand out a slug the create path could never reach.
MAX_SUFFIX = 100


class Fail(Exception):
    """A preflight or verification failure with a human-readable reason."""


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


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Report (and optionally repair) articles that share a slug."
    )
    parser.add_argument(
        "--cms-dsn",
        required=True,
        help="CMS database: user:password@tcp(host:port)/database (password optional)",
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="rename the shadowed rows; without it the script only reports",
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
        self._dir = tempfile.mkdtemp(prefix="triangle-dupe-slugs-")
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

    def execute_script(self, statements: list[str]) -> None:
        """Run many statements in one transaction, fed over stdin so the payload
        never has to fit in an argv-sized -e argument."""
        script = Path(self._dir) / "repair.sql"
        script.write_text(
            "START TRANSACTION;\n" + "\n".join(statements) + "\nCOMMIT;\n",
            encoding="utf-8",
        )
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"]]
        with script.open("rb") as handle:
            done = subprocess.run(cmd, stdin=handle, capture_output=True, text=True)
        if done.returncode != 0:
            raise Fail(f"repair failed on {self.target['database']}: {done.stderr.strip()}")


def quote(value: str) -> str:
    """Single-quoted SQL literal. These strings come from the database, so they
    get escaped rather than trusted."""
    escaped = value.replace("\\", "\\\\").replace("'", "\\'")
    return f"'{escaped}'"


def load_duplicate_groups(cms: Client) -> dict[str, list[dict]]:
    """Every row whose slug is shared, newest-last within each group."""
    rows = cms.rows(
        "SELECT a.`id`, a.`slug`, COALESCE(a.`title`, ''), "
        "  a.`pub_date` IS NOT NULL AND a.`pub_date` <= UTC_TIMESTAMP(), "
        "  a.`archived_at` IS NOT NULL "
        "FROM `articles` a "
        "JOIN (SELECT `slug` FROM `articles` GROUP BY `slug` HAVING COUNT(*) > 1) d "
        "  ON d.`slug` = a.`slug` "
        "ORDER BY a.`slug`, a.`id`"
    )
    groups: dict[str, list[dict]] = {}
    for row in rows:
        if len(row) < 5:
            continue
        groups.setdefault(row[1], []).append(
            {
                "id": int(row[0]),
                "slug": row[1],
                "title": row[2],
                "live": row[3] == "1",
                "archived": row[4] == "1",
            }
        )
    return groups


def load_all_slugs(cms: Client) -> set[str]:
    return {row[0] for row in cms.rows("SELECT `slug` FROM `articles`") if row}


def keeper_of(group: list[dict]) -> dict:
    """The row the site serves for this slug today, which is the row that keeps
    it. Mirrors GetArticle: non-archived first, then lowest id."""
    active = [row for row in group if not row["archived"]]
    return min(active or group, key=lambda row: row["id"])


def next_free_slug(base: str, taken: set[str]) -> str:
    for suffix in range(2, MAX_SUFFIX + 1):
        candidate = f"{base}-{suffix}"
        if candidate not in taken:
            return candidate
    raise Fail(f"every slug from {base}-2 to {base}-{MAX_SUFFIX} is already taken")


def describe(row: dict, keeper: bool) -> str:
    state = "archived" if row["archived"] else ("live" if row["live"] else "draft")
    marker = "keeps slug" if keeper else "SHADOWED"
    return f"#{row['id']:<7} {state:<8} {marker:<10} {row['title'][:60]}"


def main() -> int:
    args = parse_args()
    cms_client: Client | None = None

    try:
        cms_client = Client(resolve_dsn(args.cms_dsn, "cms", "CMS_DB_PASSWORD"), skip_ssl=args.skip_ssl)

        step("Scanning for shared slugs")
        groups = load_duplicate_groups(cms_client)
        if not groups:
            info("No article shares a slug with another. Nothing to do.")
            return 0
        shadowed_total = sum(len(rows) - 1 for rows in groups.values())
        info(f"{len(groups)} slug(s) shared by {shadowed_total + len(groups)} article(s)")

        step("Groups")
        taken = load_all_slugs(cms_client)
        renames: list[tuple[dict, str]] = []
        for slug, rows in sorted(groups.items()):
            keeper = keeper_of(rows)
            info(f"{slug}")
            for row in rows:
                is_keeper = row["id"] == keeper["id"]
                info(f"    {describe(row, is_keeper)}")
                if is_keeper:
                    continue
                candidate = next_free_slug(slug, taken)
                taken.add(candidate)
                renames.append((row, candidate))

        # A group whose shadowed rows are all archived costs nothing today: the
        # trash is not served. It still gets renamed, because restoring one
        # would put the collision straight back.
        live_shadowed = [row for row, _ in renames if not row["archived"] and row["live"]]
        if live_shadowed:
            warn(
                f"{len(live_shadowed)} published article(s) are unreachable on the public site: "
                "their slug serves a different article."
            )

        step("Applying" if args.apply else "Planned renames")
        for row, candidate in renames:
            info(f"#{row['id']:<7} {row['slug']}  ->  {candidate}")
        if not args.apply:
            info("")
            info("Read-only run: nothing written. Re-run with --apply to rename the shadowed rows.")
            return 0

        statements = [
            f"UPDATE `articles` SET `slug` = {quote(candidate)} WHERE `id` = {row['id']};"
            for row, candidate in renames
        ]
        cms_client.execute_script(statements)
        info(f"{len(statements)} article(s) renamed.")

        remaining = load_duplicate_groups(cms_client)
        if remaining:
            raise Fail(f"{len(remaining)} slug(s) are still shared; re-run to see them")
        info("Every article now has its own slug.")

        # article_categories and articles_authors both key on articles.id, and
        # the taxonomy counts bucket by category, so a slug rename touches
        # neither. Only the permalink moves, and only for rows that had no
        # working permalink to begin with.
        info("No index or count rebuild needed; ask the public site to purge its cache for the renamed paths.")
        return 0

    except Fail as err:
        print(f"\nERROR: {err}", file=sys.stderr)
        return 1
    finally:
        if cms_client:
            cms_client.cleanup()


if __name__ == "__main__":
    sys.exit(main())
