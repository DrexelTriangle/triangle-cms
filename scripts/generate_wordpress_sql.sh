#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GENERATE_EMBEDDINGS=0
EMBEDDING_MODEL="${WP_EMBED_MODEL:-sentence-transformers/paraphrase-MiniLM-L3-v2}"
EMBEDDING_BATCH_SIZE="${WP_EMBED_BATCH_SIZE:-64}"

POSITIONAL_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --generate-embeddings)
      GENERATE_EMBEDDINGS=1
      shift
      ;;
    --embedding-model)
      if [[ -z "${2:-}" ]]; then
        echo "missing value for --embedding-model" >&2
        exit 1
      fi
      EMBEDDING_MODEL="$2"
      shift 2
      ;;
    --embedding-batch-size)
      if [[ -z "${2:-}" ]]; then
        echo "missing value for --embedding-batch-size" >&2
        exit 1
      fi
      EMBEDDING_BATCH_SIZE="$2"
      shift 2
      ;;
    --)
      shift
      while [[ $# -gt 0 ]]; do
        POSITIONAL_ARGS+=("$1")
        shift
      done
      ;;
    -*)
      echo "unknown option: $1" >&2
      exit 1
      ;;
    *)
      POSITIONAL_ARGS+=("$1")
      shift
      ;;
  esac
done
set -- "${POSITIONAL_ARGS[@]}"

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

if [[ -n "$AUTHORS_JSON_FILE" ]]; then
  if ! jq -e '
    [to_entries[] | .value | select(.id != null)] as $rows
    | (
        ($rows | all(.login != null and (.login | type == "string") and (.login | test("^[a-z0-9]+(-[a-z0-9]+)*$"))))
        and
        (($rows | map(.login) | length) == ($rows | map(.login) | unique | length))
      )
  ' "$AUTHORS_JSON_FILE" >/dev/null; then
    cat >&2 <<'ERR'
Author slugs in wordpress-etl output are not canonical and unique.
Canonicalization/uniqueness must be owned by wordpress-etl.
Please fix upstream ETL author login slugs, then rerun this script.
ERR
    exit 1
  fi
fi

if [[ -n "$ARTICLES_JSON_FILE" ]]; then
  if ! jq -e '
    [to_entries[] | .value | select(.id != null)] as $rows
    | (
        ($rows | all(.slug != null and (.slug | type == "string") and (.slug | test("^[a-z0-9]+(-[a-z0-9]+)*$"))))
        and
        (($rows | map(.slug) | length) == ($rows | map(.slug) | unique | length))
      )
  ' "$ARTICLES_JSON_FILE" >/dev/null; then
    cat >&2 <<'ERR'
Article slugs in wordpress-etl output are not canonical and unique.
Canonicalization/uniqueness must be owned by wordpress-etl.
Please fix upstream ETL article slugs, then rerun this script.
ERR
    exit 1
  fi
fi

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
  slug LONGTEXT,
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
  excerpt LONGTEXT,
  title LONGTEXT
);
SQL

  if [[ -n "$ARTICLES_JSON_FILE" ]]; then
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
      def normalize_photo:
        if . == null then
          null
        else
          (tostring | gsub("^\\s+|\\s+$"; "")) as $photo
          | if $photo == "" then
              null
            elif ($photo | test("^(?i)https?://")) then
              $photo
            elif ($photo | startswith("//")) then
              "https:" + $photo
            elif ($photo | test("^(?i)www\\.thetriangle\\.org/")) then
              "https://" + $photo
            elif ($photo | test("^(?i)wp-content/")) then
              "https://www.thetriangle.org/" + $photo
            elif ($photo | test("^(?i)/wp-content/")) then
              "https://www.thetriangle.org" + $photo
            else
              $photo
            end
        end;
      to_entries
      | map(.value)
      | map(select(.id != null))
      | sort_by(.id)
      | .[]
      | "INSERT INTO articles (id, slug, author_ids, authors, breaking_news, comment_status, description, featured_img_id, priority, mod_date, photo_url, pub_date, tags, categories, metadata, `text`, excerpt, title) VALUES (\(.id), \((.slug // "") | sqlq), \((.authorIDs // []) | jarr), \((.authors // []) | jarr), \(if .breakingNews then 1 else 0 end), \(.commentStatus | sqlq), \(.description | sqlq), \(.featuredImgID | sqlq), \(if .priority then 1 else 0 end), \(.modDate | dt), \((.photoURL | normalize_photo) | sqlq), \(.pubDate | dt), \((.tags // []) | jarr), \((.categories // []) | jarr), \((.metadata // {}) | jarr), \(.text | sqlq), \(.excerpt | sqlq), \(.title | sqlq));"
    ' "$ARTICLES_JSON_FILE"
  elif [[ -n "$ARTICLES_SQL_FILE" ]]; then
    grep '^INSERT INTO articles ' "$ARTICLES_SQL_FILE" \
      | perl -pe 's/`authorIDs`/`author_ids`/g; s/`breakingNews`/`breaking_news`/g; s/`commentStatus`/`comment_status`/g; s/`featuredImgID`/`featured_img_id`/g; s/`modDate`/`mod_date`/g; s/`photoURL`/`photo_url`/g; s/`pubDate`/`pub_date`/g; s/'\''0000-00-00 00:00:00'\''/NULL/g'
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

# Taxonomy metadata: canonical titles for sections/subsections.
{
  cat <<'SQL'
DROP TABLE IF EXISTS site_taxonomy;
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
SQL
} > "$OUT_DIR/06-taxonomy.sql"

echo "Generated SQL in: $OUT_DIR"

if [[ "$GENERATE_EMBEDDINGS" -eq 1 ]]; then
  if [[ -z "$ARTICLES_JSON_FILE" ]]; then
    cat >&2 <<'ERR'
Embeddings generation requires article_output.json source data.
Provide a WordPress ETL directory that includes article_output.json or logs/article_output.json.
ERR
    exit 1
  fi

  python3 "$ROOT_DIR/scripts/generate_article_embeddings_sql.py" \
    --input-json "$ARTICLES_JSON_FILE" \
    --out-sql "$OUT_DIR/05-article-embeddings.sql" \
    --model "$EMBEDDING_MODEL" \
    --batch-size "$EMBEDDING_BATCH_SIZE"

  echo "Generated embeddings SQL in: $OUT_DIR/05-article-embeddings.sql"
fi
