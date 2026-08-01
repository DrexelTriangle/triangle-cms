CREATE TABLE IF NOT EXISTS classifieds (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  contact_name VARCHAR(255) NOT NULL,
  contact_email VARCHAR(255) NOT NULL,
  label VARCHAR(64) NOT NULL,
  message LONGTEXT NOT NULL,
  end_date DATE NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  submitter_ip VARCHAR(255) NULL,
  decided_at DATETIME NULL,
  decided_by VARCHAR(255) NULL,
  decided_via VARCHAR(32) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_classifieds_status_end_date (status, end_date),
  INDEX idx_classifieds_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
