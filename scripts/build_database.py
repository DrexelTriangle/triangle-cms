#!/usr/bin/env python3
"""Build a complete Triangle database on any MariaDB target, from ETL seed SQL.

Unlike scripts/reseed_from_etl.py -- which rebuilds the LOCAL Docker stack by
deleting its volume -- this talks to a target over the network, so it works
against local Docker, a scratch database, a test container, or DB1 via MaxScale.
That makes it the tool for repeatable infrastructure tests and, eventually, the
final migration.

The order below is not interchangeable. The CMS cannot bootstrap a database
from nothing: EnsureArticlesSchema is `ALTER TABLE articles ADD COLUMN IF NOT
EXISTS`, so it completes tables the ETL created rather than creating them.
Seed first, migrate second.

  1. DROP and CREATE the target database
  2. load the ETL seed SQL in order (01..08)
  3. run the CMS's own migrations against it (CMS_MIGRATE_ONLY=1)
  4. verify row counts, the id=0 article, and the CMS-owned tables

  python ./scripts/build_database.py --host 127.0.0.1 --database triangle_test
  python ./scripts/build_database.py --dsn 'u:pw@tcp(10.248.40.183:4006)/triangle_test'
  python ./scripts/build_database.py --host 10.248.40.183 --port 4006 \
      --database triangle_test --skip-migrate
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

ROOT_DIR = Path(__file__).resolve().parent.parent
SEED_DIR = ROOT_DIR / "server" / "internal" / "database" / "wordpress_etl"

SEED_FILES = (
    "01-authors.sql",
    "02-articles.sql",
    "03-articles-authors.sql",
    "04-seo.sql",
    "05-article-embeddings.sql",
    "06-taxonomy.sql",
    "07-poll-counts.sql",
    "08-comments.sql",
)

# Created by the ETL seed.
CONTENT_TABLES = ("articles", "authors", "articles_authors", "seo", "comments")
# Also from the seed, but legitimately absent: with no ETL embeddings artifact
# the generated 05-article-embeddings.sql is a placeholder whose whole body is
# DROP TABLE IF EXISTS. Missing means related-articles is empty, not a failure.
OPTIONAL_TABLES = ("article_embeddings",)
# Created by the CMS's own migrations; their absence means step 3 did not run.
CMS_TABLES = ("cms_users", "cms_sessions", "cms_settings", "media", "cms_polls", "site_taxonomy")

# DB1 and MaxScale are Delta infrastructure, NOT production -- rebuilding them
# is a legitimate and expected thing to do. They are still gated because
# `triangle` there is the live migrated dataset that everything currently reads,
# and because DB1 becomes production later, so the habit should already be in
# place. Test schemas on the same hosts are unaffected.
PROTECTED_HOSTS = {"10.248.40.154", "10.248.40.183"}
PROTECTED_DATABASES = {"triangle"}

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
    parser = argparse.ArgumentParser(description="Build a Triangle database on a MariaDB target (destructive).")
    parser.add_argument("--dsn", help="Go-style DSN: user:pass@tcp(host:port)/database (password optional)")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=3306)
    parser.add_argument("--user", default="triangle_user")
    parser.add_argument("--database", help="target schema; it will be DROPPED and recreated")
    parser.add_argument(
        "--password-env",
        default="DB_PASSWORD",
        help="env var holding the password (default: DB_PASSWORD); prompted for if unset",
    )
    parser.add_argument("--seed-dir", default=str(SEED_DIR), help=f"seed SQL directory (default: {SEED_DIR})")
    parser.add_argument(
        "--skip-migrate",
        action="store_true",
        help="load the seed but do not run the CMS migrations (leaves CMS-owned tables missing)",
    )
    parser.add_argument("--yes", "-y", action="store_true", help="skip the destructive-action confirmation")
    parser.add_argument(
        "--allow-shared-dataset",
        action="store_true",
        help="permit rebuilding the live `triangle` schema on DB1/MaxScale; refuses without it",
    )
    return parser.parse_args()


def resolve_target(args: argparse.Namespace) -> dict:
    """Merge --dsn and the discrete flags into one connection description."""
    target = {
        "host": args.host,
        "port": args.port,
        "user": args.user,
        "database": args.database,
        "password": None,
    }

    if args.dsn:
        match = DSN_RE.match(args.dsn.strip())
        if not match:
            raise Fail("--dsn must look like user:password@tcp(host:port)/database")
        parts = match.groupdict()
        target["user"] = parts["user"]
        target["host"] = parts["host"]
        target["port"] = int(parts["port"] or 3306)
        target["database"] = parts["database"]
        if parts["password"]:
            target["password"] = parts["password"]

    if not target["database"]:
        raise Fail("no target database; pass --database or include one in --dsn")

    if target["password"] is None:
        target["password"] = os.getenv(args.password_env) or ""
    if not target["password"]:
        if not sys.stdin.isatty():
            raise Fail(f"no password: set {args.password_env}, or run interactively.")
        target["password"] = getpass.getpass(f"Password for {target['user']}@{target['host']}: ")

    return target


def mysql_client() -> str:
    for candidate in ("mariadb", "mysql"):
        if shutil.which(candidate):
            return candidate
    raise Fail("neither `mariadb` nor `mysql` is on PATH; install a MariaDB client.")


class Client:
    """Runs SQL against the target without ever putting the password in argv."""

    def __init__(self, target: dict):
        self.target = target
        self.binary = mysql_client()
        # An option file keeps the password out of `ps` and shell history. 0600,
        # in a private temp dir, removed on exit.
        self._dir = tempfile.mkdtemp(prefix="triangle-build-")
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

    def _base(self) -> list[str]:
        return [self.binary, f"--defaults-file={self.defaults}"]

    def query(self, sql: str, database: str | None = None) -> str | None:
        cmd = [*self._base(), "-N", "-B", "-e", sql]
        if database:
            cmd.insert(2, database)
        done = subprocess.run(cmd, capture_output=True, text=True)
        if done.returncode != 0:
            return None
        return done.stdout.strip()

    def execute(self, sql: str, database: str | None = None) -> None:
        cmd = [*self._base(), "-e", sql]
        if database:
            cmd.insert(2, database)
        done = subprocess.run(cmd, capture_output=True, text=True)
        if done.returncode != 0:
            raise Fail(f"SQL failed ({sql[:60]}...): {done.stderr.strip()}")

    def load_file(self, path: Path, database: str) -> None:
        with path.open("rb") as handle:
            done = subprocess.run([*self._base(), database], stdin=handle, capture_output=True, text=True)
        if done.returncode != 0:
            raise Fail(f"loading {path.name} failed: {done.stderr.strip()}")


def preflight(args: argparse.Namespace, target: dict, client: Client) -> Path:
    if target["host"] in PROTECTED_HOSTS and target["database"] in PROTECTED_DATABASES:
        if not args.allow_shared_dataset:
            raise Fail(
                f"{target['host']}/{target['database']} is the live Delta dataset that the CMS "
                "currently serves. Re-run with --allow-shared-dataset if that is the intent."
            )
        warn(f"rebuilding the live Delta dataset: {target['host']}/{target['database']}")

    if client.query("SELECT 1") is None:
        raise Fail(f"cannot connect to {target['user']}@{target['host']}:{target['port']}.")

    seed_dir = Path(args.seed_dir).expanduser().resolve()
    missing = [name for name in SEED_FILES if not (seed_dir / name).is_file()]
    if missing:
        raise Fail(f"{seed_dir} is missing {', '.join(missing)}. Run scripts/generate_wordpress_sql.py first.")
    empty = [name for name in SEED_FILES if (seed_dir / name).stat().st_size == 0]
    if empty:
        raise Fail(f"{seed_dir} has empty seed files ({', '.join(empty)}); regenerate them.")

    # The id=0 article is destroyed silently without this, and it is unrecoverable.
    articles = (seed_dir / "02-articles.sql").read_text(encoding="utf-8", errors="replace")[:4096]
    if "NO_AUTO_VALUE_ON_ZERO" not in articles:
        raise Fail("02-articles.sql has no NO_AUTO_VALUE_ON_ZERO preamble; the id=0 article would be renumbered.")

    if not args.skip_migrate:
        if shutil.which("go") is None:
            raise Fail("`go` is not on PATH, which --skip-migrate avoids needing (CMS tables will be absent).")
        if not (ROOT_DIR / "server" / "main.go").is_file():
            raise Fail("server/main.go not found; run from the triangle-cms checkout.")

    return seed_dir


def confirm(args: argparse.Namespace, target: dict, client: Client, seed_dir: Path) -> None:
    existing = client.query(
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = "
        f"'{target['database']}'"
    )
    print("\n" + "=" * 72)
    print("  DESTRUCTIVE: the target database is dropped and rebuilt.")
    print("=" * 72)
    print(f"\n  Target:  {target['user']}@{target['host']}:{target['port']}/{target['database']}")
    print(f"  Seed:    {seed_dir}")
    if existing and existing.isdigit() and int(existing) > 0:
        print(f"\n  That schema currently holds {existing} tables. ALL of it is destroyed,")
        print("  including any cms_users, cms_settings and media rows.")
    else:
        print("\n  That schema does not exist yet (or is empty); nothing to lose.")
    print()

    if args.yes:
        info("--yes given; continuing without confirmation.")
        return
    if not sys.stdin.isatty():
        raise Fail("not a terminal and --yes was not given; refusing to drop a database unprompted.")
    if input(f'  Type the database name "{target["database"]}" to continue: ').strip() != target["database"]:
        raise Fail("aborted at the confirmation prompt; nothing was changed.")


def load_seed(client: Client, target: dict, seed_dir: Path) -> None:
    client.execute(f"DROP DATABASE IF EXISTS `{target['database']}`")
    client.execute(
        f"CREATE DATABASE `{target['database']}` "
        "CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
    )
    for name in SEED_FILES:
        info(f"loading {name} ...")
        client.load_file(seed_dir / name, target["database"])


def run_migrations(target: dict) -> None:
    """Let the CMS binary produce its own schema, so no DDL is duplicated here."""
    env = {
        **os.environ,
        "CMS_MIGRATE_ONLY": "1",
        "DB_NAME": target["database"],
        "DB_USER": target["user"],
        "DB_PASSWORD": target["password"],
        "DB_HOST": target["host"],
        "DB_PORT": str(target["port"]),
    }
    # A configured issuer would send the binary into OIDC discovery, which a
    # migrate-only run has no use for and which fails on an offline host.
    env.pop("OIDC_ISSUER_URL", None)

    done = subprocess.run(
        ["go", "run", "./main.go"],
        cwd=ROOT_DIR / "server",
        env=env,
        capture_output=True,
        text=True,
    )
    if done.returncode != 0 or "migrate_only_complete" not in done.stdout + done.stderr:
        tail = (done.stderr or done.stdout).strip().splitlines()[-5:]
        raise Fail("CMS migrations failed:\n    " + "\n    ".join(tail))


def verify(client: Client, target: dict, skipped_migrate: bool) -> None:
    db = target["database"]
    for table in CONTENT_TABLES:
        count = client.query(f"SELECT COUNT(*) FROM `{table}`", db)
        if count is None:
            raise Fail(f"{table} is missing after the seed load.")
        info(f"{table:<20} {count:>10}")
        if table in ("articles", "authors", "seo") and count == "0":
            raise Fail(f"{table} loaded 0 rows; the seed did not apply.")

    for table in OPTIONAL_TABLES:
        count = client.query(f"SELECT COUNT(*) FROM `{table}`", db)
        if count is None or count == "0":
            warn(f"{table} is absent or empty; /v1/articles/{{slug}} Related will be empty.")
        else:
            info(f"{table:<20} {count:>10}")

    min_id = client.query("SELECT MIN(id) FROM articles", db)
    if min_id == "0":
        info("articles.id = 0 preserved")
    else:
        warn(f"MIN(articles.id) is {min_id}, expected 0 — the id=0 row was renumbered.")

    if skipped_migrate:
        warn("--skip-migrate: CMS-owned tables were not created; the CMS will create them at first start.")
        return

    missing = [t for t in CMS_TABLES if client.query(f"SELECT 1 FROM `{t}` LIMIT 1", db) is None]
    if missing:
        raise Fail(f"CMS migrations did not create: {', '.join(missing)}")
    total = client.query(f"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='{db}'")
    info(f"{'tables total':<20} {total:>10}")


def main() -> int:
    args = parse_args()
    client = None
    try:
        target = resolve_target(args)
        client = Client(target)

        step("Preflight")
        seed_dir = preflight(args, target, client)
        info(f"target: {target['user']}@{target['host']}:{target['port']}/{target['database']}")

        confirm(args, target, client, seed_dir)

        step("Loading seed SQL")
        load_seed(client, target, seed_dir)

        if args.skip_migrate:
            step("Skipping CMS migrations (--skip-migrate)")
        else:
            step("Running CMS migrations")
            run_migrations(target)

        step("Verifying")
        verify(client, target, args.skip_migrate)

        print(f"\nDone. {target['database']} on {target['host']} is built and verified.")
        return 0
    except Fail as exc:
        print(f"\nERROR: {exc}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("\nInterrupted.", file=sys.stderr)
        return 130
    finally:
        if client:
            client.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
