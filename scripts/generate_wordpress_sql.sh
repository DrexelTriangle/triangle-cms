#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

is_valid_source_dir() {
  local dir="$1"
  [[ -f "$dir/articles.sql" && -f "$dir/articles_authors.sql" ]]
}

resolve_source_dir() {
  local base="$1"
  local candidates=("$base" "$base/logs/sql" "$base/sql")
  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ "$candidate" != /* ]]; then
      candidate="$ROOT_DIR/$candidate"
    fi
    if is_valid_source_dir "$candidate"; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

usage_error() {
  cat >&2 <<'ERR'
Could not determine WordPress ETL SQL source directory.

Expected one of:
  - first script argument
  - WP_ETL_SQL_DIR environment variable
  - ../wordpress-etl or ../wordpress-etl/logs/sql (relative to triangle-cms)
  - ./wordpress-etl or ./wordpress-etl/logs/sql (inside triangle-cms)

The resolved source must contain:
  - articles.sql
  - articles_authors.sql

Example:
  ./scripts/generate_wordpress_sql.sh ../wordpress-etl
  ./scripts/generate_wordpress_sql.sh ../wordpress-etl/logs/sql
  WP_ETL_SQL_DIR=../wordpress-etl ./scripts/generate_wordpress_sql.sh
ERR
}

# Source selection precedence:
# 1) first CLI arg (repo root or sql dir)
# 2) WP_ETL_SQL_DIR env var (repo root or sql dir)
# 3) common relative locations near this repo
SRC_HINTS=()
if [[ -n "${1:-}" ]]; then
  SRC_HINTS+=("$1")
fi
if [[ -n "${WP_ETL_SQL_DIR:-}" ]]; then
  SRC_HINTS+=("$WP_ETL_SQL_DIR")
fi
SRC_HINTS+=(
  "$ROOT_DIR/../wordpress-etl"
  "$ROOT_DIR/../wordpress-etl/logs/sql"
  "$ROOT_DIR/wordpress-etl"
  "$ROOT_DIR/wordpress-etl/logs/sql"
)

SRC_DIR=""
for hint in "${SRC_HINTS[@]}"; do
  if resolved="$(resolve_source_dir "$hint")"; then
    SRC_DIR="$resolved"
    break
  fi
done

if [[ -z "$SRC_DIR" ]]; then
  usage_error
  exit 1
fi

# Output selection precedence:
# 1) second CLI arg
# 2) WP_ETL_OUT_DIR env var
# 3) repo default
if [[ -n "${2:-}" ]]; then
  OUT_DIR="$2"
elif [[ -n "${WP_ETL_OUT_DIR:-}" ]]; then
  OUT_DIR="$WP_ETL_OUT_DIR"
else
  OUT_DIR="$ROOT_DIR/server/internal/database/wordpress_etl"
fi

# Normalize relative paths from repo root for consistent behavior.
if [[ "$OUT_DIR" != /* ]]; then
  OUT_DIR="$ROOT_DIR/$OUT_DIR"
fi

mkdir -p "$OUT_DIR"

# Authors: source CREATE statement from ETL intent, but skip malformed INSERT rows.
{
  cat <<'SQL'
DROP TABLE IF EXISTS authors;
CREATE TABLE authors (
  id BIGINT PRIMARY KEY,
  display_name VARCHAR(255) NOT NULL,
  first_name VARCHAR(255),
  last_name VARCHAR(255),
  email VARCHAR(255),
  login VARCHAR(255)
);
SQL
} > "$OUT_DIR/01-authors.sql"

# Articles: normalize column names expected by the CMS handlers.
{
  cat <<'SQL'
DROP TABLE IF EXISTS articles;
CREATE TABLE articles (
  id BIGINT PRIMARY KEY,
  author_ids LONGTEXT,
  authors LONGTEXT,
  breaking_news BOOL,
  comment_status VARCHAR(255),
  description LONGTEXT,
  featured_img_id BIGINT,
  priority BOOL,
  mod_date DATETIME,
  photo_url LONGTEXT,
  pub_date DATETIME,
  tags LONGTEXT,
  categories LONGTEXT,
  metadata LONGTEXT,
  `text` LONGTEXT,
  title LONGTEXT
);
SQL

  grep '^INSERT INTO articles ' "$SRC_DIR/articles.sql" \
    | perl -pe 's/`authorIDs`/`author_ids`/g; s/`breakingNews`/`breaking_news`/g; s/`commentStatus`/`comment_status`/g; s/`featuredImgID`/`featured_img_id`/g; s/`modDate`/`mod_date`/g; s/`photoURL`/`photo_url`/g; s/`pubDate`/`pub_date`/g; s/'\''0000-00-00 00:00:00'\''/NULL/g'
} > "$OUT_DIR/02-articles.sql"

# Article-author joins: fix the broken CREATE TABLE statement name.
{
  cat <<'SQL'
DROP TABLE IF EXISTS articles_authors;
CREATE TABLE articles_authors (
  id BIGINT PRIMARY KEY,
  author_id BIGINT NOT NULL,
  articles_id BIGINT NOT NULL
);
SQL

  grep '^INSERT INTO articles_authors ' "$SRC_DIR/articles_authors.sql"
} > "$OUT_DIR/03-articles-authors.sql"

# SEO: source CREATE statement from ETL intent, but skip malformed INSERT rows.
{
  cat <<'SQL'
DROP TABLE IF EXISTS seo;
CREATE TABLE seo (
  id BIGINT PRIMARY KEY,
  article_id BIGINT NOT NULL,
  yoast_tag_data LONGTEXT
);
SQL
} > "$OUT_DIR/04-seo.sql"

echo "Generated SQL in: $OUT_DIR"
