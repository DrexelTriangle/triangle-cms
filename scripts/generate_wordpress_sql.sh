#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

is_valid_source_dir() {
  local dir="$1"
  [[ -f "$dir/articles.sql" && -f "$dir/articles_authors.sql" ]] \
    || [[ -f "$dir/article_output.json" ]] \
    || [[ -f "$dir/merged_auth_output.json" ]] \
    || [[ -f "$dir/logs/article_output.json" ]] \
    || [[ -f "$dir/logs/merged_auth_output.json" ]]
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
  - ../wordpress-etl, ../wordpress-etl/logs, or ../wordpress-etl/logs/sql (relative to triangle-cms)
  - ./wordpress-etl, ./wordpress-etl/logs, or ./wordpress-etl/logs/sql (inside triangle-cms)

The resolved source must contain either:
  - legacy SQL files: articles.sql and articles_authors.sql
  - ETL JSON logs: article_output.json and/or merged_auth_output.json

Example:
  ./scripts/generate_wordpress_sql.sh ../wordpress-etl
  ./scripts/generate_wordpress_sql.sh ../wordpress-etl/logs
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
  "$ROOT_DIR/../wordpress-etl/logs"
  "$ROOT_DIR/../wordpress-etl/logs/sql"
  "$ROOT_DIR/wordpress-etl"
  "$ROOT_DIR/wordpress-etl/logs"
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

first_existing_file() {
  local candidate
  for candidate in "$@"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

SRC_PARENT="$(cd "$SRC_DIR/.." && pwd)"
SRC_GRANDPARENT="$(cd "$SRC_DIR/../.." && pwd)"

ARTICLES_SQL_FILE="$(first_existing_file "$SRC_DIR/articles.sql" "$SRC_PARENT/articles.sql" "$SRC_GRANDPARENT/articles.sql" || true)"
ARTICLE_AUTHORS_SQL_FILE="$(first_existing_file "$SRC_DIR/articles_authors.sql" "$SRC_PARENT/articles_authors.sql" "$SRC_GRANDPARENT/articles_authors.sql" || true)"
ARTICLES_JSON_FILE="$(first_existing_file "$SRC_DIR/article_output.json" "$SRC_DIR/logs/article_output.json" "$SRC_PARENT/article_output.json" "$SRC_PARENT/logs/article_output.json" "$SRC_GRANDPARENT/article_output.json" "$SRC_GRANDPARENT/logs/article_output.json" || true)"
AUTHORS_JSON_FILE="$(first_existing_file "$SRC_DIR/auth_output.json" "$SRC_DIR/merged_auth_output.json" "$SRC_DIR/gauth_output.json" "$SRC_DIR/logs/auth_output.json" "$SRC_DIR/logs/merged_auth_output.json" "$SRC_DIR/logs/gauth_output.json" "$SRC_PARENT/auth_output.json" "$SRC_PARENT/merged_auth_output.json" "$SRC_PARENT/gauth_output.json" "$SRC_PARENT/logs/auth_output.json" "$SRC_PARENT/logs/merged_auth_output.json" "$SRC_PARENT/logs/gauth_output.json" "$SRC_GRANDPARENT/auth_output.json" "$SRC_GRANDPARENT/merged_auth_output.json" "$SRC_GRANDPARENT/gauth_output.json" "$SRC_GRANDPARENT/logs/auth_output.json" "$SRC_GRANDPARENT/logs/merged_auth_output.json" "$SRC_GRANDPARENT/logs/gauth_output.json" || true)"

# Authors: source CREATE statement from ETL intent.
{
  cat <<'SQL'
DROP TABLE IF EXISTS authors;
CREATE TABLE authors (
  id BIGINT PRIMARY KEY,
  display_name VARCHAR(255),
  first_name VARCHAR(255),
  last_name VARCHAR(255),
  email VARCHAR(255),
  login VARCHAR(255)
);
SQL

  if [[ -n "$AUTHORS_JSON_FILE" ]]; then
    jq -r '
      def sqlq:
        if . == null then
          "NULL"
        else
          "'"'"'" + (tostring | gsub("\\\\";"\\\\\\\\") | gsub("'"'"'";"'"'"''"'"'")) + "'"'"'"
        end;
      to_entries
      | map(.value)
      | map(select(.id != null))
      | sort_by(.id)
      | .[]
      | "INSERT INTO authors (id, display_name, first_name, last_name, email, login) VALUES (\(.id), \(.display_name | sqlq), \(.first_name | sqlq), \(.last_name | sqlq), \(.email | sqlq), \(.login | sqlq));"
    ' "$AUTHORS_JSON_FILE"
  elif [[ -n "$ARTICLES_SQL_FILE" && -f "$(dirname "$ARTICLES_SQL_FILE")/authors.sql" ]]; then
    grep '^INSERT INTO authors ' "$(dirname "$ARTICLES_SQL_FILE")/authors.sql"
  else
    echo "-- No author source found; generated schema only."
  fi
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

  if [[ -n "$ARTICLES_SQL_FILE" ]]; then
    grep '^INSERT INTO articles ' "$ARTICLES_SQL_FILE" \
      | perl -pe 's/`authorIDs`/`author_ids`/g; s/`breakingNews`/`breaking_news`/g; s/`commentStatus`/`comment_status`/g; s/`featuredImgID`/`featured_img_id`/g; s/`modDate`/`mod_date`/g; s/`photoURL`/`photo_url`/g; s/`pubDate`/`pub_date`/g; s/'\''0000-00-00 00:00:00'\''/NULL/g'
  elif [[ -n "$ARTICLES_JSON_FILE" ]]; then
    jq -r '
      def sqlq:
        if . == null then
          "NULL"
        else
          "'"'"'" + (tostring | gsub("\\\\";"\\\\\\\\") | gsub("'"'"'";"'"'"''"'"'")) + "'"'"'"
        end;
      def dt:
        if . == null or . == "" or . == "0000-00-00 00:00:00" then "NULL" else sqlq end;
      def jarr:
        if . == null then "NULL" else (tojson | sqlq) end;

      to_entries
      | map(.value)
      | map(select(.id != null))
      | sort_by(.id)
      | .[]
      | "INSERT INTO articles (id, author_ids, authors, breaking_news, comment_status, description, featured_img_id, priority, mod_date, photo_url, pub_date, tags, categories, metadata, `text`, title) VALUES (\(.id), \((.authorIDs // []) | jarr), \((.authors // []) | jarr), \(if .breakingNews then 1 else 0 end), \(.commentStatus | sqlq), \(.description | sqlq), \(.featuredImgID | sqlq), \(if .priority then 1 else 0 end), \(.modDate | dt), \(.photoURL | sqlq), \(.pubDate | dt), \((.tags // []) | jarr), \((.categories // []) | jarr), \((.metadata // {}) | jarr), \(.text | sqlq), \(.title | sqlq));"
    ' "$ARTICLES_JSON_FILE"
  else
    echo "-- No articles source found; generated schema only."
  fi
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

  if [[ -n "$ARTICLE_AUTHORS_SQL_FILE" ]]; then
    grep '^INSERT INTO articles_authors ' "$ARTICLE_AUTHORS_SQL_FILE"
  elif [[ -n "$ARTICLES_JSON_FILE" ]]; then
    jq -r '
      [
        to_entries[]
        | .value as $article
        | ($article.authorIDs // [])[]
        | select(type == "number")
        | {author_id: ., articles_id: $article.id}
      ]
      | to_entries[]
      | "INSERT INTO articles_authors (id, author_id, articles_id) VALUES (\(.key + 1), \(.value.author_id), \(.value.articles_id));"
    ' "$ARTICLES_JSON_FILE"
  else
    echo "-- No article-author source found; generated schema only."
  fi
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
