#!/usr/bin/env python3
"""Fill `legacy_redirects` with a redirect for every published WordPress post.

When the legacy WordPress is switched off, cms.thetriangle.org moves to Delta,
whose nginx answers the old URLs from this table (GET /v1/legacy-redirects,
turned into nginx maps by sync-legacy-redirects; see triangle-infrastructure
docs/legacy-redirects.md). This script is what fills it: it reads the WordPress
database directly, works out each post's canonical permalink exactly the way
WordPress did, finds the same article in the CMS, and writes three rows per
post:

  path     /news/academic-transformation/<post_name>/  -> /article/<cms slug>
  post_id  67825 (the ?p= shortlink)                    -> /article/<cms slug>
  slug     <post_name>                                  -> /article/<cms slug>

The slug row is for the other spellings WordPress accepted and 301'd to the
canonical one (a post filed under a second category, an attachment page under
it); nginx falls back to it when no path row matches.

  python ./scripts/build_legacy_redirects.py \\
      --wp-dsn 'wordpressuser@tcp(10.248.42.122:3306)/wordpress' \\
      --cms-dsn 'triangle_user@tcp(10.248.40.183:4006)/triangle' --skip-ssl

Passwords come from WP_DB_PASSWORD / CMS_DB_PASSWORD (or a prompt), never from
argv. Read-only by default: it prints what it would write and the posts it
could not place, and exits. Pass --apply to replace the generated rows.

MATCHING. The CMS renumbered every article and rewrote some slugs on import
(percent-encoded curly quotes became dashes; colliding slugs got the article
id appended), so neither the id nor the slug can be carried over. A post is
matched on its GMT publish time, which the import copied verbatim, and ties
are broken on the title. Posts with no publish time, or whose time matches
nothing, fall back to the slug and then to a unique title within a day. What
still matches nothing is reported, never guessed at.

RE-RUNNABLE. --apply deletes every `wp_post` row and inserts the new set in
one transaction, so a second run converges on the same table. Rows with any
other origin (`manual`) are never touched, and a generated row whose source a
manual row already claims is skipped, so a hand-written redirect always wins.

WordPress only has to be readable, not serving: run this once more against the
final data after the PHP site is switched off, before its database is dropped.
"""

from __future__ import annotations

import argparse
import getpass
import html
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unicodedata
from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path

DSN_RE = re.compile(
    r"^(?P<user>[^:@/]+)(?::(?P<password>[^@]*))?@tcp\((?P<host>[^:)]+)(?::(?P<port>\d+))?\)/(?P<database>[^?]+)"
)
TABLE_PREFIX_RE = re.compile(r"^[A-Za-z0-9_]+$")

ORIGIN = "wp_post"
INSERT_BATCH = 500

# The permalink tags this script can expand. WordPress has a few more (%hour%,
# %minute%, %second%, %author%); a structure using one of those fails loudly
# instead of producing paths that look right and match nothing.
SUPPORTED_TAGS = {"%year%", "%monthnum%", "%day%", "%postname%", "%post_id%", "%category%"}
TAG_RE = re.compile(r"%[a-z_]+%")
ZERO_DATE = "0000-00-00 00:00:00"


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


# --- database access -------------------------------------------------------


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
        self._dir = tempfile.mkdtemp(prefix="triangle-legacy-redirects-")
        self.defaults = Path(self._dir) / "my.cnf"
        self.defaults.write_text(
            "[client]\n"
            f"host={target['host']}\n"
            f"port={target['port']}\n"
            f"user={target['user']}\n"
            f"password={target['password']}\n"
            "default-character-set=utf8mb4\n"
            + ("ssl=0\n" if skip_ssl else ""),
            encoding="utf-8",
        )
        self.defaults.chmod(0o600)

    def cleanup(self) -> None:
        shutil.rmtree(self._dir, ignore_errors=True)

    def rows(self, sql: str) -> list[list[str]]:
        """Tab-separated rows. Free text must be selected through HEX() and read
        back with unhex(): batch mode escapes tabs, newlines and backslashes,
        and titles contain all three."""
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"], "-N", "-B", "-e", sql]
        done = subprocess.run(cmd, capture_output=True, text=True, check=False)
        if done.returncode != 0:
            raise Fail(f"query failed on {self.target['database']}: {done.stderr.strip()}")
        return [line.split("\t") for line in done.stdout.splitlines() if line]

    def execute_script(self, statements: list[str]) -> None:
        """Run many statements in one transaction, fed over stdin so the payload
        never has to fit in an argv-sized -e argument."""
        script = Path(self._dir) / "apply.sql"
        script.write_text(
            "START TRANSACTION;\n" + "\n".join(statements) + "\nCOMMIT;\n",
            encoding="utf-8",
        )
        cmd = [self.binary, f"--defaults-file={self.defaults}", self.target["database"]]
        with script.open("rb") as handle:
            done = subprocess.run(cmd, stdin=handle, capture_output=True, text=True, check=False)
        if done.returncode != 0:
            raise Fail(f"apply failed on {self.target['database']}: {done.stderr.strip()}")


def unhex(value: str) -> str:
    if value in ("", "NULL"):
        return ""
    return bytes.fromhex(value).decode("utf-8", errors="replace")


def sql_text(value: str) -> str:
    """A string literal that needs no escaping: the values come from two
    databases and are written into a third statement, so they are passed as hex
    rather than trusted to survive quoting."""
    if value == "":
        return "''"
    return f"CONVERT(X'{value.encode('utf-8').hex()}' USING utf8mb4)"


def sql_int(value: int | None) -> str:
    return "NULL" if value is None else str(int(value))


# --- WordPress -------------------------------------------------------------


@dataclass
class WPPost:
    id: int
    post_name: str
    post_date: str  # site-local time; what the permalink's date tags use
    post_date_gmt: str  # what the CMS import copied into articles.pub_date
    title: str
    categories: list[int]
    primary_category: int | None


@dataclass
class WPCategory:
    term_id: int
    slug: str
    parent: int


@dataclass
class CMSArticle:
    id: int
    slug: str
    pub_date: str
    title: str
    archived: bool


@dataclass
class Redirect:
    source_kind: str
    source: str
    target: str
    status: int
    wp_object_id: int
    article_id: int | None
    note: str


def wp_option(wp: Client, prefix: str, name: str) -> str:
    rows = wp.rows(f"SELECT HEX(option_value) FROM {prefix}options WHERE option_name = '{name}'")
    return unhex(rows[0][0]) if rows else ""


def load_wp_categories(wp: Client, prefix: str) -> dict[int, WPCategory]:
    rows = wp.rows(
        f"SELECT t.term_id, HEX(t.slug), tt.parent FROM {prefix}terms t "
        f"JOIN {prefix}term_taxonomy tt ON tt.term_id = t.term_id WHERE tt.taxonomy = 'category'"
    )
    return {int(r[0]): WPCategory(int(r[0]), unhex(r[1]), int(r[2])) for r in rows}


def load_wp_posts(wp: Client, prefix: str) -> list[WPPost]:
    rows = wp.rows(
        f"SELECT ID, HEX(post_name), post_date, post_date_gmt, HEX(post_title) FROM {prefix}posts "
        "WHERE post_type = 'post' AND post_status = 'publish' ORDER BY ID"
    )
    posts = {
        int(r[0]): WPPost(int(r[0]), unhex(r[1]), r[2], r[3], unhex(r[4]), [], None)
        for r in rows
    }

    for post_id, term_id in wp.rows(
        f"SELECT tr.object_id, tt.term_id FROM {prefix}term_relationships tr "
        f"JOIN {prefix}term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id "
        "WHERE tt.taxonomy = 'category'"
    ):
        post = posts.get(int(post_id))
        if post:
            post.categories.append(int(term_id))

    # Yoast's primary category overrides WordPress's pick for %category%.
    for post_id, value in wp.rows(
        f"SELECT post_id, meta_value FROM {prefix}postmeta WHERE meta_key = '_yoast_wpseo_primary_category'"
    ):
        post = posts.get(int(post_id))
        if post and value.strip().isdigit():
            post.primary_category = int(value.strip())

    return list(posts.values())


def category_path(term_id: int, categories: dict[int, WPCategory]) -> str:
    """parent/child/grandchild, as get_category_parents() builds it."""
    parts: list[str] = []
    seen: set[int] = set()
    current = categories.get(term_id)
    while current and current.term_id not in seen:
        seen.add(current.term_id)
        parts.append(current.slug)
        current = categories.get(current.parent) if current.parent else None
    return "/".join(reversed(parts))


def permalink_category(post: WPPost, categories: dict[int, WPCategory], default_category: int | None) -> int | None:
    """The category WordPress put in a post's permalink.

    get_permalink() sorts the post's categories by term id and takes the first;
    Yoast's post_link_category filter swaps in the primary category when it is
    set and still assigned to the post. A post with no category at all falls
    back to the site's default_category.
    """
    assigned = sorted(t for t in post.categories if t in categories)
    if post.primary_category is not None and post.primary_category in assigned:
        return post.primary_category
    if assigned:
        return assigned[0]
    return default_category


def permalink_path(structure: str, post: WPPost, category: str) -> str:
    unknown = set(TAG_RE.findall(structure)) - SUPPORTED_TAGS
    if unknown:
        raise Fail(f"permalink_structure uses unsupported tags: {', '.join(sorted(unknown))}")

    date = post.post_date if post.post_date and post.post_date != ZERO_DATE else post.post_date_gmt
    year, month, day = date[0:4], date[5:7], date[8:10]
    path = (
        structure.replace("%year%", year)
        .replace("%monthnum%", month)
        .replace("%day%", day)
        .replace("%postname%", post.post_name)
        .replace("%post_id%", str(post.id))
        .replace("%category%", category)
    )
    path = re.sub(r"/{2,}", "/", path)
    return path if path.startswith("/") else "/" + path


# --- CMS -------------------------------------------------------------------


def load_cms_articles(cms: Client) -> list[CMSArticle]:
    """Published articles, archived ones included (they become 410s). Drafts
    and scheduled articles are left out: a redirect to one would 404, and one
    sharing a WordPress post's publish second would steal its match."""
    rows = cms.rows(
        "SELECT `id`, HEX(`slug`), `pub_date`, HEX(COALESCE(`title`, '')), "
        "`archived_at` IS NOT NULL FROM `articles` "
        "WHERE `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP()"
    )
    return [CMSArticle(int(r[0]), unhex(r[1]), r[2], unhex(r[3]), r[4] == "1") for r in rows]


def redirects_table_exists(cms: Client) -> bool:
    """The CMS creates the table at startup, so it is missing only on a
    database the new CMS has not booted against yet."""
    rows = cms.rows(
        "SELECT COUNT(*) FROM information_schema.tables "
        "WHERE table_schema = DATABASE() AND table_name = 'legacy_redirects'"
    )
    return rows[0][0] == "1"


def load_manual_sources(cms: Client) -> set[tuple[str, str]]:
    rows = cms.rows(
        f"SELECT `source_kind`, HEX(`source`) FROM `legacy_redirects` WHERE `origin` <> '{ORIGIN}'"
    )
    return {(r[0], unhex(r[1])) for r in rows}


# --- matching --------------------------------------------------------------


QUOTES = str.maketrans({"‘": "'", "’": "'", "“": '"', "”": '"', "–": "-", "—": "-"})


def normalize_title(title: str) -> str:
    """Titles went through two encodings (WordPress stored some as entities, the
    import decoded others), so compare them with all of that stripped away."""
    text = html.unescape(html.unescape(title or ""))
    text = unicodedata.normalize("NFKC", text).translate(QUOTES).lower()
    return " ".join(re.sub(r"[^\w]+", " ", text).split())


def cms_slug_candidates(post_name: str) -> list[str]:
    """The slugs the import could have given this post: unchanged, or with
    every % turned into a dash (how it canonicalized percent-encoded
    characters)."""
    candidates = [post_name]
    if "%" in post_name:
        candidates.append(post_name.replace("%", "-").strip("-"))
    return candidates


def gmt(value: str) -> datetime:
    """A DATETIME column as printed by the client. Both sides are GMT."""
    return datetime.strptime(value[:19], "%Y-%m-%d %H:%M:%S").replace(tzinfo=timezone.utc)


class Matcher:
    def __init__(self, articles: list[CMSArticle]):
        self.by_date: dict[str, list[CMSArticle]] = defaultdict(list)
        self.by_slug: dict[str, CMSArticle] = {}
        self.by_title: dict[str, list[CMSArticle]] = defaultdict(list)
        for article in articles:
            if article.pub_date:
                self.by_date[article.pub_date].append(article)
            # Lowest id wins, the same row the CMS serves for a shared slug.
            if article.slug not in self.by_slug or article.id < self.by_slug[article.slug].id:
                self.by_slug[article.slug] = article
            self.by_title[normalize_title(article.title)].append(article)

    def match(self, post: WPPost) -> tuple[CMSArticle | None, str]:
        title = normalize_title(post.title)
        has_date = bool(post.post_date_gmt) and post.post_date_gmt != ZERO_DATE

        if has_date:
            same_time = self.by_date.get(post.post_date_gmt, [])
            if len(same_time) == 1:
                # Still a match -- editors retitle articles -- but counted
                # apart so a dry run shows how much rests on the time alone.
                if normalize_title(same_time[0].title) != title:
                    return same_time[0], "date, retitled"
                return same_time[0], "date"
            titled = [a for a in same_time if normalize_title(a.title) == title]
            if len(titled) == 1:
                return titled[0], "date+title"

        for slug in cms_slug_candidates(post.post_name):
            if slug in self.by_slug:
                return self.by_slug[slug], "slug"

        if has_date:
            published = gmt(post.post_date_gmt)
            near = [
                a
                for a in self.by_title.get(title, [])
                if a.pub_date and abs(gmt(a.pub_date) - published) <= timedelta(days=1)
            ]
            if len(near) == 1:
                return near[0], "title"

        return None, "unmatched"


def build_redirects(
    posts: list[WPPost],
    categories: dict[int, WPCategory],
    default_category: int | None,
    structure: str,
    matcher: Matcher,
    manual: set[tuple[str, str]],
) -> tuple[list[Redirect], list[tuple[WPPost, str]], dict[str, int]]:
    redirects: list[Redirect] = []
    unplaced: list[tuple[WPPost, str]] = []
    stats: dict[str, int] = defaultdict(int)
    claimed: dict[tuple[str, str], int] = {}
    # The edge's slug fallback reads the last path segment, which for /news/
    # or /news/academic-transformation/ is a category. A post sharing that
    # name would swallow the section page, so it gets no slug row; its path
    # and post_id rows still cover it.
    category_slugs = {c.slug.lower() for c in categories.values()}

    for post in posts:
        if not post.post_name:
            unplaced.append((post, "no post_name"))
            continue

        article, how = matcher.match(post)
        stats[how] += 1
        if article is None:
            unplaced.append((post, "no matching CMS article"))
            continue

        term = permalink_category(post, categories, default_category)
        path = permalink_path(structure, post, category_path(term, categories) if term else "uncategorized")

        # An article taken down in the CMS is gone, not moved.
        status, target, note = (410, "", "archived in CMS") if article.archived else (301, f"/article/{article.slug}", how)
        if article.archived:
            stats["archived"] += 1

        for kind, source in (("path", path.lower()), ("post_id", str(post.id)), ("slug", post.post_name.lower())):
            key = (kind, source)
            if kind == "slug" and source in category_slugs:
                stats["slug_is_category"] += 1
                continue
            if key in manual:
                stats["shadowed_by_manual"] += 1
                continue
            if key in claimed:
                warn(f"{kind} {source!r} is claimed by posts {claimed[key]} and {post.id}; keeping {claimed[key]}")
                stats["duplicate_source"] += 1
                continue
            claimed[key] = post.id
            redirects.append(Redirect(kind, source, target, status, post.id, article.id, note))

    return redirects, unplaced, stats


def apply_statements(redirects: list[Redirect]) -> list[str]:
    statements = [f"DELETE FROM `legacy_redirects` WHERE `origin` = '{ORIGIN}';"]
    for start in range(0, len(redirects), INSERT_BATCH):
        values = ",\n".join(
            "(" + ", ".join([
                sql_text(r.source_kind),
                sql_text(r.source),
                sql_text(r.target),
                str(r.status),
                f"'{ORIGIN}'",
                sql_int(r.wp_object_id),
                sql_int(r.article_id),
                sql_text(r.note[:255]),
            ]) + ")"
            for r in redirects[start:start + INSERT_BATCH]
        )
        statements.append(
            "INSERT INTO `legacy_redirects` "
            "(`source_kind`, `source`, `target`, `status`, `origin`, `wp_object_id`, `article_id`, `note`) "
            f"VALUES\n{values};"
        )
    return statements


# --- main ------------------------------------------------------------------


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build legacy_redirects from the WordPress database (dry run unless --apply)."
    )
    parser.add_argument("--wp-dsn", required=True, help="WordPress database: user:password@tcp(host:port)/database")
    parser.add_argument("--cms-dsn", required=True, help="CMS database: user:password@tcp(host:port)/database")
    parser.add_argument("--wp-table-prefix", default="wp_", help="WordPress $table_prefix (default: wp_)")
    parser.add_argument("--skip-ssl", action="store_true", help="Connect without TLS (MaxScale on the private network)")
    parser.add_argument("--report", type=Path, help="Write every redirect and every unplaced post to this TSV")
    parser.add_argument("--apply", action="store_true", help="Replace the generated rows in legacy_redirects")
    return parser.parse_args()


def write_report(path: Path, redirects: list[Redirect], unplaced: list[tuple[WPPost, str]]) -> None:
    with path.open("w", encoding="utf-8") as out:
        out.write("kind\tsource\ttarget\tstatus\twp_post_id\tarticle_id\tnote\n")
        for r in redirects:
            out.write(f"{r.source_kind}\t{r.source}\t{r.target}\t{r.status}\t{r.wp_object_id}\t{r.article_id}\t{r.note}\n")
        for post, reason in unplaced:
            title = post.title.replace("\t", " ").replace("\n", " ")
            out.write(f"unplaced\t{post.post_name}\t\t\t{post.id}\t\t{reason}: {title} ({post.post_date_gmt})\n")


def main() -> int:
    args = parse_args()
    if not TABLE_PREFIX_RE.match(args.wp_table_prefix):
        raise Fail("--wp-table-prefix may only contain letters, digits and underscores")
    prefix = args.wp_table_prefix

    wp = Client(resolve_dsn(args.wp_dsn, "wp", "WP_DB_PASSWORD"), skip_ssl=args.skip_ssl)
    cms = Client(resolve_dsn(args.cms_dsn, "cms", "CMS_DB_PASSWORD"), skip_ssl=args.skip_ssl)
    try:
        step("Reading WordPress")
        structure = wp_option(wp, prefix, "permalink_structure")
        if not structure:
            raise Fail("permalink_structure is empty: the site used ?p= URLs only, which this script does not model")
        default = wp_option(wp, prefix, "default_category")
        default_category = int(default) if default.isdigit() else None
        categories = load_wp_categories(wp, prefix)
        posts = load_wp_posts(wp, prefix)
        info(f"permalink structure: {structure}")
        info(f"{len(posts)} published posts, {len(categories)} categories")

        step("Reading the CMS")
        articles = load_cms_articles(cms)
        if redirects_table_exists(cms):
            manual = load_manual_sources(cms)
        elif args.apply:
            raise Fail("legacy_redirects does not exist yet: deploy the CMS version that creates it, then re-run")
        else:
            warn("legacy_redirects does not exist yet (the CMS creates it at startup); assuming no manual rows")
            manual = set()
        info(f"{len(articles)} articles, {len(manual)} manual redirects")

        step("Matching")
        redirects, unplaced, stats = build_redirects(
            posts, categories, default_category, structure, Matcher(articles), manual
        )
        for key in ("date", "date, retitled", "date+title", "slug", "title", "unmatched", "archived", "shadowed_by_manual", "slug_is_category", "duplicate_source"):
            if stats.get(key):
                info(f"{key:>20}: {stats[key]}")
        info(f"{len(redirects)} redirects for {len(posts) - len(unplaced)} posts; {len(unplaced)} posts unplaced")
        for post, reason in unplaced[:20]:
            info(f"unplaced {post.id} {post.post_name!r}: {reason}")
        if len(unplaced) > 20:
            info(f"... and {len(unplaced) - 20} more (use --report for the full list)")
        for r in redirects[:6]:
            info(f"e.g. {r.source_kind} {r.source} -> {r.target or r.status}")

        if args.report:
            write_report(args.report, redirects, unplaced)
            info(f"report written to {args.report}")

        if not args.apply:
            step("Dry run: nothing written. Re-run with --apply to replace the generated rows.")
            return 0

        step("Applying")
        cms.execute_script(apply_statements(redirects))
        # MaxScale can route the read-back of a write to a replica that has not
        # caught up, and ROW_COUNT() is unreliable through it, so count again
        # and say so rather than trust the transaction's own report.
        written = int(cms.rows(f"SELECT COUNT(*) FROM `legacy_redirects` WHERE `origin` = '{ORIGIN}'")[0][0])
        info(f"{written} generated rows now in legacy_redirects (expected {len(redirects)})")
        if written != len(redirects):
            warn("the count differs; if this went through MaxScale, re-check in a few seconds before assuming a failure")
        return 0
    finally:
        wp.cleanup()
        cms.cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Fail as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        sys.exit(1)
