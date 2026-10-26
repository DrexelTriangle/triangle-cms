CREATE TABLE IF NOT EXISTS newsletter_links (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  newsletter_id BIGINT UNSIGNED NOT NULL,
  url VARCHAR(2048) NOT NULL,
  url_hash CHAR(64) NOT NULL,
  click_count INT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL,
  UNIQUE KEY uq_newsletter_links_url (newsletter_id, url_hash),
  CONSTRAINT fk_newsletter_links_newsletter FOREIGN KEY (newsletter_id) REFERENCES newsletters (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
