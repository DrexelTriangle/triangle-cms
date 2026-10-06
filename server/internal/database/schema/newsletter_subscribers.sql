CREATE TABLE IF NOT EXISTS newsletter_subscribers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(254) NOT NULL,
  name VARCHAR(100) NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'subscribed',
  token CHAR(43) NOT NULL,
  source VARCHAR(32) NOT NULL,
  signup_ip VARCHAR(45) NULL,
  subscribed_at DATETIME NOT NULL,
  unsubscribed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_newsletter_subscribers_email (email),
  UNIQUE KEY uq_newsletter_subscribers_token (token),
  INDEX idx_newsletter_subscribers_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
