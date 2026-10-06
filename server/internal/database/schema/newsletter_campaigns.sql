CREATE TABLE IF NOT EXISTS newsletter_campaigns (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  subject VARCHAR(255) NOT NULL,
  preview_text VARCHAR(255) NULL,
  body_blocks LONGTEXT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'draft',
  scheduled_at DATETIME NULL,
  sent_at DATETIME NULL,
  recipient_count INT UNSIGNED NULL,
  created_by VARCHAR(255) NULL,
  updated_by VARCHAR(255) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_newsletter_campaigns_status_scheduled (status, scheduled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
