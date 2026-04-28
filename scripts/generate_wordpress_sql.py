#!/usr/bin/env python3
"""Copy WordPress ETL SQL artifacts into CMS bootstrap SQL filenames."""

from __future__ import annotations

import argparse
import os
import re
import shutil
import sys
from pathlib import Path


TAXONOMY_SQL = """DROP TABLE IF EXISTS site_taxonomy;
CREATE TABLE site_taxonomy (
  id BIGINT PRIMARY KEY,
  kind VARCHAR(32) NOT NULL,
  slug VARCHAR(255) NOT NULL,
  canonical_title VARCHAR(255) NOT NULL,
  parent_slug VARCHAR(255) NULL,
  UNIQUE KEY uq_site_taxonomy_kind_slug (kind, slug)
);

INSERT INTO site_taxonomy (id, kind, slug, canonical_title, parent_slug) VALUES
  (1, 'section', 'news', 'News', NULL),
  (2, 'section', 'sports', 'Sports', NULL),
  (3, 'section', 'opinion', 'Opinion', NULL),
  (4, 'section', 'columns', 'Columns', NULL),
  (5, 'section', 'entertainment', 'Entertainment', NULL),
  (6, 'section', 'comics-puzzles', 'Comics & Puzzles', NULL),
  (7, 'subsection', 'academic-transformation', 'Academic Transformation', 'news'),
  (8, 'subsection', 'politics', 'Politics', 'news'),
  (9, 'subsection', 'transit', 'Transit', 'news'),
  (10, 'subsection', 'crime-policy-violations', 'Crime & Policy Violations', 'news'),
  (11, 'subsection', 'mens-basketball', 'Men''s Basketball', 'sports'),
  (12, 'subsection', 'womens-basketball', 'Women''s Basketball', 'sports'),
  (13, 'subsection', 'big-5', 'Big 5', 'sports'),
  (14, 'subsection', 'philly-sports', 'Philly Sports', 'sports'),
  (15, 'subsection', 'field-hockey', 'Field Hockey', 'sports'),
  (16, 'subsection', 'mens-soccer', 'Men''s Soccer', 'sports'),
  (17, 'subsection', 'womens-soccer', 'Women''s Soccer', 'sports'),
  (18, 'subsection', 'science-tech', 'Science & Tech', 'opinion'),
  (19, 'subsection', 'from-the-editor', 'From the Editor', 'opinion'),
  (20, 'subsection', 'the-love-triangle', 'The Love Triangle', 'columns'),
  (21, 'subsection', 'tri-this-sweet-treat', 'Tri This Sweet Treat', 'columns'),
  (22, 'subsection', 'movies', 'Movies', 'entertainment'),
  (23, 'subsection', 'music', 'Music', 'entertainment'),
  (24, 'subsection', 'happening-in-philly', 'Happening in Philly', 'entertainment'),
  (25, 'subsection', 'cooking', 'Cooking', 'entertainment'),
  (26, 'subsection', 'books', 'Books', 'entertainment'),
  (27, 'subsection', 'gaming', 'Gaming', 'entertainment'),
  (28, 'subsection', 'listicles', 'Listicles', 'entertainment'),
  (29, 'subsection', 'political-cartoons', 'Political Cartoons', 'comics-puzzles'),
  (30, 'subsection', 'crossword', 'Crossword', 'comics-puzzles'),
  (31, 'subsection', 'sudoku', 'Sudoku', 'comics-puzzles');
"""

PLACEHOLDER_EMBEDDINGS_SQL = """DROP TABLE IF EXISTS article_embeddings;
-- No article_embeddings.sql found in ETL output.
"""

NO_AUTO_VALUE_ON_ZERO_PREAMBLE = "SET sql_mode = CONCAT(@@sql_mode, ',NO_AUTO_VALUE_ON_ZERO');\n"

USAGE_ERROR = """Could not determine WordPress ETL SQL source directory.

Expected one of:
  - first script argument
  - WP_ETL_SQL_DIR environment variable
  - ../wordpress-etl, ../wordpress-etl/logs, or ../wordpress-etl/logs/sql (relative to triangle-cms)
  - ./wordpress-etl, ./wordpress-etl/logs, or ./wordpress-etl/logs/sql (inside triangle-cms)

The resolved source must contain ETL SQL files:
  - authors.sql
  - articles.sql
  - articles_authors.sql
  - seo.sql

Optional ETL SQL file:
  - article_embeddings.sql

Examples:
  python ./scripts/generate_wordpress_sql.py ../wordpress-etl
  python ./scripts/generate_wordpress_sql.py ../wordpress-etl/logs
  python ./scripts/generate_wordpress_sql.py ../wordpress-etl/logs/sql
  WP_ETL_SQL_DIR=../wordpress-etl python ./scripts/generate_wordpress_sql.py
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Copies SQL artifacts produced by wordpress-etl into CMS bootstrap filenames. "
            "No WordPress data transformation is performed."
        )
    )
    parser.add_argument("source_dir", nargs="?", default=None)
    parser.add_argument("output_dir", nargs="?", default=None)
    return parser.parse_args()


def is_valid_source_dir(path: Path) -> bool:
    required = ("authors.sql", "articles.sql", "articles_authors.sql", "seo.sql")
    return all((path / name).is_file() for name in required)


def resolve_source_dir(root_dir: Path, base_hint: str) -> Path | None:
    base = Path(base_hint)
    if not base.is_absolute():
        base = root_dir / base
    candidates = (base, base / "logs" / "sql", base / "sql")
    for candidate in candidates:
        if is_valid_source_dir(candidate):
            return candidate.resolve()
    return None


def first_existing_file(candidates: list[Path]) -> Path | None:
    for path in candidates:
        if path.is_file():
            return path.resolve()
    return None


def require_file(path: Path | None, label: str) -> Path:
    if path is None:
        print(f"missing required ETL SQL artifact: {label}", file=sys.stderr)
        print(USAGE_ERROR, file=sys.stderr)
        raise SystemExit(1)
    return path


def ensure_pattern(path: Path, pattern: str, label: str) -> None:
    text = path.read_text(encoding="utf-8", errors="replace")
    if re.search(pattern, text) is None:
        print(
            f"ETL artifact is not in CMS-compatible format: {label} ({path})",
            file=sys.stderr,
        )
        print("Run the latest wordpress-etl pipeline to regenerate SQL artifacts.", file=sys.stderr)
        raise SystemExit(1)


def copy_sql_with_mariadb_mode(src: Path, dest: Path) -> None:
    text = src.read_text(encoding="utf-8", errors="replace")
    if "NO_AUTO_VALUE_ON_ZERO" not in text:
        text = NO_AUTO_VALUE_ON_ZERO_PREAMBLE + text
    dest.write_text(text, encoding="utf-8")


def main() -> int:
    args = parse_args()
    root_dir = Path(__file__).resolve().parent.parent

    src_hints: list[str] = []
    if args.source_dir:
        src_hints.append(args.source_dir)
    env_src = os.getenv("WP_ETL_SQL_DIR")
    if env_src:
        src_hints.append(env_src)
    src_hints.extend(
        [
            str(root_dir / ".." / "wordpress-etl"),
            str(root_dir / ".." / "wordpress-etl" / "logs"),
            str(root_dir / ".." / "wordpress-etl" / "logs" / "sql"),
            str(root_dir / "wordpress-etl"),
            str(root_dir / "wordpress-etl" / "logs"),
            str(root_dir / "wordpress-etl" / "logs" / "sql"),
        ]
    )

    src_dir: Path | None = None
    for hint in src_hints:
        resolved = resolve_source_dir(root_dir, hint)
        if resolved is not None:
            src_dir = resolved
            break

    if src_dir is None:
        print(USAGE_ERROR, file=sys.stderr)
        return 1

    if args.output_dir:
        out_dir = Path(args.output_dir)
    else:
        out_dir = Path(os.getenv("WP_ETL_OUT_DIR", str(root_dir / "server" / "internal" / "database" / "wordpress_etl")))
    if not out_dir.is_absolute():
        out_dir = root_dir / out_dir
    out_dir = out_dir.resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    src_parent = src_dir.parent.resolve()
    src_grandparent = src_parent.parent.resolve()

    authors_sql = first_existing_file(
        [
            src_dir / "authors.sql",
            src_parent / "authors.sql",
            src_parent / "logs" / "sql" / "authors.sql",
            src_parent / "sql" / "authors.sql",
            src_grandparent / "authors.sql",
            src_grandparent / "logs" / "sql" / "authors.sql",
            src_grandparent / "sql" / "authors.sql",
        ]
    )
    articles_sql = first_existing_file(
        [
            src_dir / "articles.sql",
            src_parent / "articles.sql",
            src_parent / "logs" / "sql" / "articles.sql",
            src_parent / "sql" / "articles.sql",
            src_grandparent / "articles.sql",
            src_grandparent / "logs" / "sql" / "articles.sql",
            src_grandparent / "sql" / "articles.sql",
        ]
    )
    article_authors_sql = first_existing_file(
        [
            src_dir / "articles_authors.sql",
            src_parent / "articles_authors.sql",
            src_parent / "logs" / "sql" / "articles_authors.sql",
            src_parent / "sql" / "articles_authors.sql",
            src_grandparent / "articles_authors.sql",
            src_grandparent / "logs" / "sql" / "articles_authors.sql",
            src_grandparent / "sql" / "articles_authors.sql",
        ]
    )
    seo_sql = first_existing_file(
        [
            src_dir / "seo.sql",
            src_parent / "seo.sql",
            src_parent / "logs" / "sql" / "seo.sql",
            src_parent / "sql" / "seo.sql",
            src_grandparent / "seo.sql",
            src_grandparent / "logs" / "sql" / "seo.sql",
            src_grandparent / "sql" / "seo.sql",
        ]
    )
    embeddings_sql = first_existing_file(
        [
            src_dir / "article_embeddings.sql",
            src_parent / "article_embeddings.sql",
            src_parent / "logs" / "sql" / "article_embeddings.sql",
            src_parent / "sql" / "article_embeddings.sql",
            src_grandparent / "article_embeddings.sql",
            src_grandparent / "logs" / "sql" / "article_embeddings.sql",
            src_grandparent / "sql" / "article_embeddings.sql",
        ]
    )

    authors_sql = require_file(authors_sql, "authors.sql")
    articles_sql = require_file(articles_sql, "articles.sql")
    article_authors_sql = require_file(article_authors_sql, "articles_authors.sql")
    seo_sql = require_file(seo_sql, "seo.sql")

    ensure_pattern(authors_sql, r"`login`| login ", "authors.login column")
    ensure_pattern(articles_sql, r"`author_ids`| author_ids ", "articles.author_ids column")
    ensure_pattern(articles_sql, r"`comment_status`| comment_status ", "articles.comment_status column")
    ensure_pattern(article_authors_sql, r"`author_id`| author_id ", "articles_authors.author_id column")
    ensure_pattern(seo_sql, r"`yoast_tag_data`| yoast_tag_data ", "seo.yoast_tag_data column")

    out_authors = out_dir / "01-authors.sql"
    out_articles = out_dir / "02-articles.sql"
    out_article_authors = out_dir / "03-articles-authors.sql"
    out_seo = out_dir / "04-seo.sql"
    out_embeddings = out_dir / "05-article-embeddings.sql"
    out_taxonomy = out_dir / "06-taxonomy.sql"

    copy_sql_with_mariadb_mode(authors_sql, out_authors)
    copy_sql_with_mariadb_mode(articles_sql, out_articles)
    shutil.copyfile(article_authors_sql, out_article_authors)
    shutil.copyfile(seo_sql, out_seo)

    if embeddings_sql is not None:
        shutil.copyfile(embeddings_sql, out_embeddings)
    else:
        out_embeddings.write_text(PLACEHOLDER_EMBEDDINGS_SQL, encoding="utf-8")

    out_taxonomy.write_text(TAXONOMY_SQL, encoding="utf-8")

    print(f"Imported ETL SQL into: {out_dir}")
    print(f"  01-authors.sql <- {authors_sql}")
    print(f"  02-articles.sql <- {articles_sql}")
    print(f"  03-articles-authors.sql <- {article_authors_sql}")
    print(f"  04-seo.sql <- {seo_sql}")
    if embeddings_sql is not None:
        print(f"  05-article-embeddings.sql <- {embeddings_sql}")
    else:
        print("  05-article-embeddings.sql <- placeholder (no ETL embeddings artifact found)")
    print("  06-taxonomy.sql <- cms static taxonomy seed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
