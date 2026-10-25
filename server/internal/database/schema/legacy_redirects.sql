CREATE TABLE IF NOT EXISTS legacy_redirects (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  -- What `source` is matched against on the old WordPress host:
  --   path    : the request path, e.g. /news/academic-transformation/some-slug/
  --   post_id : the value of ?p= (WordPress shortlinks), e.g. 67825
  --   slug    : a post's last path segment, for the category spellings
  --             WordPress also accepted (/sports/<slug>/ for a /news/ post)
  source_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  -- Stored exactly as WordPress stored it: lowercase, with any non-ASCII
  -- character percent-encoded (post_name is never decoded in wp_posts). ASCII
  -- keeps the unique key inside a 767-byte index prefix, and the nginx map
  -- sync decodes it into the form nginx's $uri compares against.
  source VARCHAR(750) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  -- A path on www.thetriangle.org (/article/<slug>), empty when status is 410.
  target VARCHAR(1024) NOT NULL DEFAULT '',
  status SMALLINT UNSIGNED NOT NULL DEFAULT 301,
  -- Who owns the row. scripts/build_legacy_redirects.py rewrites only the
  -- rows it generates (wp_post); `manual` rows are never touched by it, so a
  -- hand-added redirect survives every rebuild. The default makes a row
  -- someone inserts by hand manual without their having to know this.
  origin VARCHAR(32) NOT NULL DEFAULT 'manual',
  wp_object_id BIGINT UNSIGNED NULL,
  article_id BIGINT NULL,
  note VARCHAR(255) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_legacy_redirects_source (source_kind, source),
  INDEX idx_legacy_redirects_origin (origin)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
