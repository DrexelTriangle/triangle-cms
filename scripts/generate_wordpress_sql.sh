#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="${1:-/home/sachin/Documents/Coding/wordpress-etl/logs/sql}"
OUT_DIR="${2:-server/internal/database/wordpress_etl}"

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
