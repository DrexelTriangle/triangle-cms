#!/usr/bin/env python3
"""Copy WordPress's hand-picked photo-gallery selection into the CMS.

The public /photo page is curated, not "every image we have". In WordPress that
selection lives on each attachment as the post meta `include_in_gallery`, which
the legacy `triangle/v1/gallery` endpoint filtered on; in the CMS it is the
`media.in_gallery` column. Nothing in the ETL carries it: CMS media rows come
from walking the CephFS uploads tree, which knows only what is on disk. Without
this backfill the gallery starts empty after a cutover.

  python ./scripts/backfill_gallery_flags.py \
      --wp-dsn wordpress@tcp(10.248.42.122)/wordpress \
      --cms-dsn triangle_user@tcp(10.248.40.154)/triangle --dry-run

Passwords come from WP_DB_PASSWORD and CMS_DB_PASSWORD (or a prompt), never from
argv. Re-runnable: it sets the flag on what WordPress has marked and, unless
--additive, clears it on everything else, so the CMS ends up matching WordPress
exactly. Marks made in the CMS since the last run are therefore lost unless you
pass --additive, which is the right choice once editors have started curating
in the CMS and WordPress is only the historical seed.
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

# WordPress stores `_wp_attached_file` relative to the uploads directory
# ("2026/01/photo.jpg"); the CMS stores paths relative to MEDIA_ROOT, which
# keeps WordPress's own layout underneath this prefix.
UPLOADS_PREFIX = "wp-content/uploads/"

GALLERY_META_KEY = "include_in_gallery"

# WordPress writes booleans as "1"/"0", but a meta row touched by other plugins
# can hold "true" or "yes". Anything else (including "0" and the empty string)
# means not in the gallery.
TRUTHY_META = ("1", "true", "yes", "on")

DSN_RE = re.compile(
    r"^(?P<user>[^:@/]+)(?::(?P<password>[^@]*))?@tcp\((?P<host>[^:)]+)(?::(?P<port>\d+))?\)/(?P<database>[^?]+)"
)


class Fail(Exception):
    """A preflight or verification failure with a human-readable reason."""


def info(msg: str) -> None:
    print(f"  {msg}")


def step(msg: str) -> None:
    print(f"\n==> {msg}")


def warn(msg: str) -> None:
    print(f"  WARNING: {msg}", file=sys.stderr)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Copy WordPress's include_in_gallery selection into the CMS media library."
    )
    parser.add_argument(
        "--wp-dsn",
        required=True,
        help="legacy WordPress database: user:password@tcp(host:port)/database (password optional)",
    )
    parser.add_argument(
        "--cms-dsn",
        required=True,
        help="CMS database: user:password@tcp(host:port)/database (password optional)",
    )
    parser.add_argument(
        "--wp-prefix",
        default="wp_",
        help="WordPress table prefix (default: wp_)",
    )
    parser.add_argument(
        "--additive",
        action="store_true",
        help="only set flags; leave images already marked in the CMS alone",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="report what would change without writing anything",
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

    def __init__(self, target: dict):
        self.target = target
        self.binary = mysql_client()
        # An option file keeps the password out of `ps` and shell history. 0600,
        # in a private temp dir, removed on exit.
        self._dir = tempfile.mkdtemp(prefix="triangle-gallery-")
        self.defaults = Path(self._dir) / "my.cnf"
        self.defaults.write_text(
            "[client]\n"
            f"host={target['host']}\n"
            f"port={target['port']}\n"
            f"user={target['user']}\n"
            f"password={target['password']}\n",
            encoding="utf-8",
        )
        self.defaults.chmod(0o600)

    def cleanup(self) -> None:
        shutil.rmtree(self._dir, ignore_errors=True)

    def _run(self, sql: str, *, tab_separated: bool) -> subprocess.CompletedProcess:
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"]]
        if tab_separated:
            cmd += ["-N", "-B"]
        cmd += ["-e", sql]
        return subprocess.run(cmd, capture_output=True, text=True)

    def rows(self, sql: str) -> list[list[str]]:
        done = self._run(sql, tab_separated=True)
        if done.returncode != 0:
            raise Fail(f"query failed on {self.target['database']}: {done.stderr.strip()}")
        return [line.split("\t") for line in done.stdout.splitlines() if line]

    def scalar(self, sql: str) -> str:
        rows = self.rows(sql)
        return rows[0][0] if rows and rows[0] else ""

    def execute(self, sql: str) -> None:
        done = self._run(sql, tab_separated=False)
        if done.returncode != 0:
            raise Fail(f"statement failed on {self.target['database']}: {done.stderr.strip()}")


def quote(value: str) -> str:
    """Single-quoted SQL literal. Paths are file names, but they come from a
    database, so they get escaped rather than trusted."""
    return "'" + value.replace("\\", "\\\\").replace("'", "''") + "'"


def selected_paths(wp: Client, prefix: str) -> list[str]:
    """Uploads-relative file paths WordPress has marked for the gallery."""
    truthy = ", ".join(quote(value) for value in TRUTHY_META)
    rows = wp.rows(
        "SELECT DISTINCT attached.meta_value "
        f"FROM {prefix}postmeta AS flag "
        f"JOIN {prefix}postmeta AS attached "
        "  ON attached.post_id = flag.post_id AND attached.meta_key = '_wp_attached_file' "
        f"JOIN {prefix}posts AS post "
        "  ON post.ID = flag.post_id AND post.post_type = 'attachment' "
        f"WHERE flag.meta_key = {quote(GALLERY_META_KEY)} "
        f"  AND LOWER(flag.meta_value) IN ({truthy}) "
        "ORDER BY attached.meta_value"
    )
    return [row[0].strip() for row in rows if row and row[0].strip()]


def main() -> int:
    args = parse_args()
    wp_client: Client | None = None
    cms_client: Client | None = None

    try:
        wp_target = resolve_dsn(args.wp_dsn, "wp", "WP_DB_PASSWORD")
        cms_target = resolve_dsn(args.cms_dsn, "cms", "CMS_DB_PASSWORD")
        wp_client = Client(wp_target)
        cms_client = Client(cms_target)

        step("Reading the WordPress selection")
        paths = selected_paths(wp_client, args.wp_prefix)
        if not paths:
            raise Fail(
                f"no attachments carry {GALLERY_META_KEY}; check --wp-prefix "
                f"(currently {args.wp_prefix!r}) and that this is the live WordPress database."
            )
        info(f"{len(paths)} attachment(s) marked for the gallery in WordPress")

        # The CMS knows nothing about WordPress attachment ids, so the file path
        # is the join key. An unmatched path means the file never made it onto
        # the media mount, or the library has not been reindexed since it did.
        cms_paths = [UPLOADS_PREFIX + path.lstrip("/") for path in paths]
        literals = ", ".join(quote(path) for path in cms_paths)

        step("Matching against the CMS media library")
        matched = {row[0] for row in cms_client.rows(f"SELECT `path` FROM `media` WHERE `path` IN ({literals})")}
        missing = [path for path in cms_paths if path not in matched]
        info(f"{len(matched)} of {len(cms_paths)} found in the library")
        if missing:
            warn(f"{len(missing)} marked image(s) are not in the library and will be skipped:")
            for path in missing[:10]:
                info(f"  {path}")
            if len(missing) > 10:
                info(f"  ... and {len(missing) - 10} more")
            info("Run Reindex Media in Settings if these files are on the media mount.")

        already = int(cms_client.scalar("SELECT COUNT(*) FROM `media` WHERE `in_gallery` = 1") or 0)
        to_clear = 0 if args.additive else int(
            cms_client.scalar(
                f"SELECT COUNT(*) FROM `media` WHERE `in_gallery` = 1 AND `path` NOT IN ({literals})"
            )
            or 0
        )

        step("Applying")
        info(f"{already} image(s) currently in the CMS gallery")
        info(f"{len(matched)} to be marked; {to_clear} to be unmarked")
        if args.dry_run:
            info("--dry-run: nothing written.")
            return 0

        if not args.additive:
            cms_client.execute(f"UPDATE `media` SET `in_gallery` = 0 WHERE `path` NOT IN ({literals})")
        cms_client.execute(f"UPDATE `media` SET `in_gallery` = 1 WHERE `path` IN ({literals})")

        total = int(cms_client.scalar("SELECT COUNT(*) FROM `media` WHERE `in_gallery` = 1") or 0)
        info(f"{total} image(s) now on the public photo gallery")
        return 0

    except Fail as err:
        print(f"\nERROR: {err}", file=sys.stderr)
        return 1
    finally:
        if wp_client:
            wp_client.cleanup()
        if cms_client:
            cms_client.cleanup()


if __name__ == "__main__":
    sys.exit(main())
