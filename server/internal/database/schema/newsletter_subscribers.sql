CREATE TABLE IF NOT EXISTS newsletter_subscribers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(255) NOT NULL,
  name VARCHAR(255) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'confirmed',
  token CHAR(32) NOT NULL,
  legacy_wp_id BIGINT UNSIGNED NULL,
  legacy_wp_token VARCHAR(64) NULL,
  subscribed_at DATETIME NOT NULL,
  unsubscribed_at DATETIME NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uq_newsletter_subscribers_email (email),
  UNIQUE KEY uq_newsletter_subscribers_token (token),
  UNIQUE KEY uq_newsletter_subscribers_legacy_wp_id (legacy_wp_id),
  INDEX idx_newsletter_subscribers_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
