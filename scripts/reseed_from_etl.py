#!/usr/bin/env python3
"""Rebuild the local dev database from the WordPress ETL, embeddings included.

End to end:

  1. run the wordpress-etl pipeline (optionally generating article embeddings)
  2. copy its SQL artifacts into server/internal/database/wordpress_etl/
  3. DESTROY the local MariaDB volume and bring it back up, so Docker's
     docker-entrypoint-initdb.d replays the seed from scratch
  4. verify what actually landed, and restart the CMS so its startup migrations
     run against the fresh schema

Step 3 is destructive and irreversible: the seed files are mounted into
docker-entrypoint-initdb.d, which MariaDB runs *only* on a first-time init, so a
genuine re-seed means throwing the data directory away. Everything local goes
with it; see the warning block this prints before it touches anything.

  python ./scripts/reseed_from_etl.py                 # full run, with prompts
  python ./scripts/reseed_from_etl.py --skip-etl      # reuse existing ETL SQL
  python ./scripts/reseed_from_etl.py --no-embeddings # skip the slow model step
  python ./scripts/reseed_from_etl.py --yes           # no confirmation prompt
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

ROOT_DIR = Path(__file__).resolve().parent.parent
SEED_DIR = ROOT_DIR / "server" / "internal" / "database" / "wordpress_etl"
COMPOSE_VOLUME = "mariadb_data"

# Seed files, in the order docker-entrypoint-initdb.d applies them.
SEED_FILES = (
    "01-authors.sql",
    "02-articles.sql",
    "03-articles-authors.sql",
    "04-seo.sql",
    "05-article-embeddings.sql",
    "06-taxonomy.sql",
    "07-poll-counts.sql",
    "08-comments.sql",
    # Polls before their options: the options table's foreign key needs the
    # parent rows to exist.
    "09-polls.sql",
    "10-poll-options.sql",
)

# Row counts worth reporting, and the shape a healthy seed has.
VERIFY_TABLES = (
    "articles",
    "authors",
    "articles_authors",
    "seo",
    "comments",
    "article_embeddings",
    "site_taxonomy",
    "cms_polls",
    "cms_poll_options",
)


class Fail(Exception):
    """A preflight or verification failure with a human-readable reason."""


def info(msg: str) -> None:
    print(f"  {msg}")


def step(msg: str) -> None:
    print(f"\n==> {msg}")


def warn(msg: str) -> None:
    print(f"  WARNING: {msg}", file=sys.stderr)


def run(cmd: list[str], *, cwd: Path, capture: bool = False, check: bool = True) -> subprocess.CompletedProcess:
    if capture:
        return subprocess.run(cmd, cwd=cwd, check=check, text=True, capture_output=True)
    return subprocess.run(cmd, cwd=cwd, check=check)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Rebuild the local dev database from the WordPress ETL (destructive).",
    )
    parser.add_argument(
        "--etl-dir",
        default=os.getenv("WP_ETL_DIR", str(ROOT_DIR.parent / "wordpress-etl")),
        help="wordpress-etl checkout (default: ../wordpress-etl)",
    )
    parser.add_argument(
        "--skip-etl",
        action="store_true",
        help="do not re-run the pipeline; reuse the SQL already in the ETL's logs/sql",
    )
    parser.add_argument(
        "--no-embeddings",
        action="store_true",
        help="skip embedding generation (minutes faster; /v1/articles/{slug} Related stays empty)",
    )
    parser.add_argument(
        "--interactive-authors",
        action="store_true",
        help="prompt on ambiguous author matches instead of passing --best-guess to the ETL",
    )
    parser.add_argument(
        "--yes",
        "-y",
        action="store_true",
        help="skip the destructive-action confirmation prompt",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=900,
        help="seconds to wait for MariaDB to finish loading the seed (default: 900)",
    )
    return parser.parse_args()


# --------------------------------------------------------------------------
# Preflight
# --------------------------------------------------------------------------


def resolve_compose() -> list[str]:
    if shutil.which("docker") is None:
        raise Fail("docker is not on PATH.")
    probe = run(["docker", "compose", "version"], cwd=ROOT_DIR, capture=True, check=False)
    if probe.returncode == 0:
        return ["docker", "compose"]
    if shutil.which("docker-compose"):
        return ["docker-compose"]
    raise Fail("neither `docker compose` nor `docker-compose` is available.")


def preflight(args: argparse.Namespace) -> tuple[Path, Path | None]:
    """Fail fast, before anything destructive happens."""
    if not (ROOT_DIR / "docker-compose.yml").is_file():
        raise Fail(f"{ROOT_DIR} does not look like the triangle-cms checkout.")

    # compose interpolates these with `:?`, so a missing .env fails only *after*
    # the volume is already gone, which would leave no database at all.
    env_file = ROOT_DIR / ".env"
    if not env_file.is_file():
        raise Fail(".env is missing; compose needs MARIADB_ROOT_PASSWORD and MARIADB_PASSWORD.")
    env_text = env_file.read_text(encoding="utf-8")
    for key in ("MARIADB_ROOT_PASSWORD", "MARIADB_PASSWORD"):
        if f"{key}=" not in env_text:
            raise Fail(f".env has no {key}; compose would refuse to start MariaDB.")

    if run(["docker", "info"], cwd=ROOT_DIR, capture=True, check=False).returncode != 0:
        raise Fail("the Docker daemon is not reachable.")

    generator = ROOT_DIR / "scripts" / "generate_wordpress_sql.py"
    if not generator.is_file():
        raise Fail(f"missing {generator}.")

    etl_dir = Path(args.etl_dir).expanduser().resolve()
    etl_python: Path | None = None
    if not args.skip_etl:
        if not (etl_dir / "main.py").is_file():
            raise Fail(f"no wordpress-etl checkout at {etl_dir} (pass --etl-dir, or --skip-etl).")

        # The pipeline reads the WordPress export; without it the run dies deep
        # into the pipeline rather than here.
        if not any((etl_dir / "Data").glob("*.zip")):
            raise Fail(f"no WordPress export zip in {etl_dir / 'Data'}.")

        # Prefer the ETL's own venv: sentence-transformers and textual are
        # usually installed there and not in the system interpreter.
        candidate = etl_dir / ".venv" / "bin" / "python"
        etl_python = candidate if candidate.is_file() else Path(sys.executable)

        missing = modules_missing(etl_python, ["textual"])
        if missing:
            raise Fail(f"{etl_python} cannot import {', '.join(missing)}; install the ETL requirements.")

        if not args.no_embeddings:
            missing = modules_missing(etl_python, ["sentence_transformers"])
            if missing:
                raise Fail(
                    f"{etl_python} cannot import sentence_transformers, which --generate-embeddings needs. "
                    "Install the ETL requirements, or re-run with --no-embeddings."
                )

    return etl_dir, etl_python


def modules_missing(python: Path, modules: list[str]) -> list[str]:
    missing = []
    for module in modules:
        probe = subprocess.run(
            [str(python), "-c", f"import {module}"],
            capture_output=True,
            text=True,
        )
        if probe.returncode != 0:
            missing.append(module)
    return missing


# --------------------------------------------------------------------------
# Database helpers
# --------------------------------------------------------------------------


def compose_volume_name() -> str | None:
    """Resolve the real volume name via compose labels, not a guessed prefix."""
    probe = run(
        [
            "docker",
            "volume",
            "ls",
            "--filter",
            f"label=com.docker.compose.volume={COMPOSE_VOLUME}",
            "--format",
            "{{.Name}}\t{{.Label \"com.docker.compose.project\"}}",
        ],
        cwd=ROOT_DIR,
        capture=True,
        check=False,
    )
    project = os.getenv("COMPOSE_PROJECT_NAME", ROOT_DIR.name)
    for line in probe.stdout.splitlines():
        name, _, vol_project = line.partition("\t")
        if vol_project == project:
            return name
    return None


def mariadb_query(compose: list[str], sql: str) -> str | None:
    """Run SQL as root inside the container. None if it is not queryable."""
    probe = run(
        [*compose, "exec", "-T", "mariadb", "sh", "-c",
         f'exec mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" "$MARIADB_DATABASE" -N -B -e {shell_quote(sql)}'],
        cwd=ROOT_DIR,
        capture=True,
        check=False,
    )
    if probe.returncode != 0:
        return None
    return probe.stdout.strip()


def shell_quote(value: str) -> str:
    return "'" + value.replace("'", "'\\''") + "'"


def snapshot_counts(compose: list[str]) -> dict[str, str]:
    """Best-effort row counts, for showing what is about to be destroyed."""
    counts: dict[str, str] = {}
    for table in VERIFY_TABLES:
        result = mariadb_query(compose, f"SELECT COUNT(*) FROM `{table}`")
        if result is not None and result.isdigit():
            counts[table] = result
    return counts


# --------------------------------------------------------------------------
# Warning / confirmation
# --------------------------------------------------------------------------


def embeddings_outlook(args: argparse.Namespace, etl_dir: Path) -> tuple[bool, str]:
    """Will this run end up with article embeddings, and why/why not?

    Answered BEFORE the destructive confirmation. The seed always replaces
    article_embeddings, with a real table or with a placeholder that drops it,
    so "no embeddings" is a decision to destroy the ones you have, and the
    user has to be able to see that while they can still say no.
    """
    if args.no_embeddings:
        return False, "--no-embeddings was passed, so none will be generated"

    existing = etl_dir / "logs" / "sql" / "article_embeddings.sql"
    if not args.skip_etl:
        return True, "the ETL will generate them (--generate-embeddings)"

    # --skip-etl: whatever is already on disk is what gets loaded.
    if not existing.is_file():
        return False, f"--skip-etl was passed and {existing} does not exist"
    if "VECTOR" not in existing.read_text(encoding="utf-8", errors="replace")[:4096]:
        return False, f"--skip-etl was passed and {existing.name} has no VECTOR column"
    return True, f"reusing {existing.name} ({existing.stat().st_size // 1024} KiB)"


def confirm(
    args: argparse.Namespace,
    compose: list[str],
    volume: str | None,
    etl_dir: Path,
) -> None:
    print("\n" + "=" * 72)
    print("  DESTRUCTIVE: this rebuilds the local dev database from scratch.")
    print("=" * 72)
    print("\n  About to permanently delete the local MariaDB data directory")
    print(f"  (docker volume: {volume or '<not created yet>'}).")
    print("\n  Everything in the local database goes, not just imported content:")
    print("    - articles, authors, SEO rows and comments")
    print("    - cms_users and cms_sessions   -> you will be logged out of the local CMS")
    print("    - cms_settings                 -> site title, SEO defaults, breaking news")
    print("    - polls, poll options and their recorded votes")
    print("    - the media catalogue          -> files on disk survive; the DB rows do not")
    print("\n  There is no backup and no undo. Anything you created by hand in the")
    print("  local CMS and care about should be exported first.")

    counts = snapshot_counts(compose)
    if counts:
        print("\n  Current contents (all of this is destroyed):")
        for table, count in counts.items():
            print(f"    {table:<20} {count:>10}")
    else:
        print("\n  (The database is not currently queryable, so nothing to report.)")

    # Stated here, not after the ETL runs: by then the database is already gone
    # and there is nothing left to decide.
    will_embed, why = embeddings_outlook(args, etl_dir)
    if will_embed:
        print(f"\n  Embeddings: YES ({why}).")
    else:
        current = counts.get("article_embeddings")
        print(f"\n  Embeddings: NO ({why}).")
        if current and current != "0":
            print(f"    The seed replaces article_embeddings, so the {current} rows you have now")
            print("    are destroyed and NOT regenerated. Related-articles on")
            print("    /v1/articles/{slug} will come back empty until you re-run with embeddings.")
        else:
            print("    Related-articles on /v1/articles/{slug} will be empty.")

    print("\n  NOT affected: production, Loki/Grafana volumes, and files on disk.")
    print("  This script only ever talks to the local Docker stack.\n")

    if args.yes:
        info("--yes given; continuing without confirmation.")
        return

    if not sys.stdin.isatty():
        raise Fail("not a terminal and --yes was not given; refusing to destroy data unprompted.")

    reply = input('  Type "reseed" to continue, anything else to abort: ').strip()
    if reply != "reseed":
        raise Fail("aborted at the confirmation prompt; nothing was changed.")


# --------------------------------------------------------------------------
# Pipeline steps
# --------------------------------------------------------------------------


def run_etl(args: argparse.Namespace, etl_dir: Path, etl_python: Path) -> None:
    cmd = [str(etl_python), "main.py"]
    if not args.no_embeddings:
        cmd.append("--generate-embeddings")
    if not args.interactive_authors:
        # Otherwise the pipeline blocks on author-conflict prompts.
        cmd.append("--best-guess")

    info(f"{' '.join(cmd)}   (cwd: {etl_dir})")
    info("This opens the ETL's terminal UI. Embedding generation can take several minutes.")
    result = run(cmd, cwd=etl_dir, check=False)
    if result.returncode != 0:
        raise Fail(f"the ETL pipeline exited {result.returncode}; the database was NOT touched.")


def check_etl_artifacts(args: argparse.Namespace, etl_dir: Path) -> None:
    sql_dir = etl_dir / "logs" / "sql"
    required = ["authors.sql", "articles.sql", "articles_authors.sql", "seo.sql"]
    missing = [name for name in required if not (sql_dir / name).is_file()]
    if missing:
        raise Fail(f"ETL produced no {', '.join(missing)} in {sql_dir}.")

    embeddings = sql_dir / "article_embeddings.sql"
    if args.no_embeddings:
        warn("--no-embeddings: related-articles on /v1/articles/{slug} will come back empty.")
    elif not embeddings.is_file():
        warn(f"no {embeddings.name} was produced; related-articles will be unavailable.")
    elif "VECTOR" not in embeddings.read_text(encoding="utf-8", errors="replace")[:4096]:
        warn(f"{embeddings.name} has no VECTOR column; related-articles will be unavailable.")
    else:
        info(f"embeddings present ({embeddings.stat().st_size // 1024} KiB)")


def generate_seed(etl_dir: Path) -> None:
    run([sys.executable, str(ROOT_DIR / "scripts" / "generate_wordpress_sql.py"), str(etl_dir)], cwd=ROOT_DIR)

    missing = [name for name in SEED_FILES if not (SEED_DIR / name).is_file()]
    if missing:
        raise Fail(f"seed generation did not produce {', '.join(missing)}.")

    # The generator enforces this, but the id=0 row is unrecoverable once it is
    # renumbered, so confirm on the file that is actually about to be loaded.
    articles = (SEED_DIR / "02-articles.sql").read_text(encoding="utf-8", errors="replace")[:4096]
    if "NO_AUTO_VALUE_ON_ZERO" not in articles:
        raise Fail("02-articles.sql has no NO_AUTO_VALUE_ON_ZERO preamble; the id=0 article would be renumbered.")


def recreate_database(compose: list[str], volume: str | None) -> None:
    info("stopping cms and mariadb")
    run([*compose, "stop", "cms", "mariadb"], cwd=ROOT_DIR, check=False)
    run([*compose, "rm", "-f", "-s", "-v", "mariadb"], cwd=ROOT_DIR, check=False)

    if volume:
        info(f"removing volume {volume}")
        removed = run(["docker", "volume", "rm", volume], cwd=ROOT_DIR, capture=True, check=False)
        if removed.returncode != 0:
            raise Fail(f"could not remove {volume}: {removed.stderr.strip()}")
    else:
        info("no existing volume; MariaDB will initialise a new one")

    info("starting mariadb (this replays the seed via docker-entrypoint-initdb.d)")
    run([*compose, "up", "-d", "mariadb"], cwd=ROOT_DIR)


def wait_for_seed(compose: list[str], timeout: int) -> None:
    """Wait until the seed has actually finished loading, not just until the port opens."""
    deadline = time.time() + timeout
    last_count = None
    while time.time() < deadline:
        count = mariadb_query(compose, "SELECT COUNT(*) FROM articles")
        if count is not None and count.isdigit() and int(count) > 0:
            info(f"seed loaded ({count} articles)")
            return
        if count != last_count:
            info("waiting for the seed to load...")
            last_count = count
        time.sleep(5)
    raise Fail(
        f"MariaDB did not finish seeding within {timeout}s. "
        f"Check `docker compose logs mariadb`; a syntax error in a seed file leaves the DB empty."
    )


def verify(compose: list[str]) -> dict[str, str]:
    results: dict[str, str] = {}
    for table in VERIFY_TABLES:
        count = mariadb_query(compose, f"SELECT COUNT(*) FROM `{table}`")
        results[table] = count if count is not None and count.isdigit() else "MISSING"

    for table in ("articles", "authors", "seo"):
        if results.get(table) in ("MISSING", "0"):
            raise Fail(f"{table} is {results.get(table)} after seeding; the load did not work.")

    # articles.id = 0 is a real row and only survives under NO_AUTO_VALUE_ON_ZERO.
    min_id = mariadb_query(compose, "SELECT MIN(id) FROM articles")
    if min_id == "0":
        info("articles.id = 0 preserved")
    else:
        warn(f"MIN(articles.id) is {min_id}, expected 0; the id=0 row was renumbered.")

    if results.get("article_embeddings") in ("MISSING", "0"):
        warn("article_embeddings is empty; /v1/articles/{slug} Related will be empty.")

    if results.get("cms_polls") in ("MISSING", "0"):
        warn(
            "cms_polls is empty; /pollsarchive will have nothing in it. The ETL only "
            "emits polls once its scripts/dump_wp_polls.sh has written the wp-polls "
            "tables into Data/."
        )

    return results


def restart_cms(compose: list[str]) -> None:
    """Bring the CMS back so its Ensure* startup migrations run on the fresh DB."""
    info("starting cms (runs the startup schema migrations)")
    result = run([*compose, "up", "-d", "cms"], cwd=ROOT_DIR, check=False)
    if result.returncode != 0:
        warn("cms did not start; run `docker compose up -d cms` yourself and check its logs.")
        return

    deadline = time.time() + 120
    while time.time() < deadline:
        probe = run(
            [*compose, "ps", "--format", "json", "cms"],
            cwd=ROOT_DIR,
            capture=True,
            check=False,
        )
        for line in probe.stdout.splitlines():
            try:
                state = json.loads(line)
            except json.JSONDecodeError:
                continue
            health = (state.get("Health") or "").lower()
            if health == "healthy":
                info("cms healthy")
                return
            if health == "unhealthy":
                warn("cms reports unhealthy; check `docker compose logs cms`.")
                return
        time.sleep(5)
    warn("cms did not report healthy within 120s; check `docker compose logs cms`.")


def main() -> int:
    args = parse_args()

    try:
        compose = resolve_compose()
        step("Preflight")
        etl_dir, etl_python = preflight(args)
        info(f"repo:    {ROOT_DIR}")
        info(f"etl:     {etl_dir}{' (skipped)' if args.skip_etl else ''}")
        info(f"seed to: {SEED_DIR}")

        volume = compose_volume_name()
        confirm(args, compose, volume, etl_dir)

        if args.skip_etl:
            step("Reusing existing ETL SQL (--skip-etl)")
        else:
            step("Running the wordpress-etl pipeline")
            assert etl_python is not None
            run_etl(args, etl_dir, etl_python)

        step("Checking ETL artifacts")
        check_etl_artifacts(args, etl_dir)

        step("Generating seed SQL")
        generate_seed(etl_dir)

        step("Recreating the database")
        # Re-resolve: the volume may not have existed at preflight time.
        recreate_database(compose, volume or compose_volume_name())

        step("Waiting for the seed to load")
        wait_for_seed(compose, args.timeout)

        step("Verifying")
        results = verify(compose)
        for table, count in results.items():
            info(f"{table:<20} {count:>10}")

        step("Restarting the CMS")
        restart_cms(compose)

        print("\nDone. The local database has been rebuilt from the ETL.")
        print("  API:      curl -k https://localhost:8080/v1/health/db")
        print("  Reminder: local CMS users and settings were wiped; sign in again to recreate yours.")
        return 0

    except Fail as exc:
        print(f"\nERROR: {exc}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("\nInterrupted.", file=sys.stderr)
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
