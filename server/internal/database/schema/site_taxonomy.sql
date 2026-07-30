CREATE TABLE IF NOT EXISTS site_taxonomy (
  id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  kind VARCHAR(32) NOT NULL,
  slug VARCHAR(255) NOT NULL,
  canonical_title VARCHAR(255) NOT NULL,
  parent_slug VARCHAR(255) NULL,
  article_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  UNIQUE KEY uq_site_taxonomy_kind_slug (kind, slug),
  KEY idx_site_taxonomy_kind (kind),
  KEY idx_site_taxonomy_parent_slug (parent_slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
