CREATE TABLE IF NOT EXISTS comments (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  article_id BIGINT NULL,
  wp_post_id BIGINT NULL,
  parent_id BIGINT NULL,
  author_name LONGTEXT,
  author_email LONGTEXT,
  author_url LONGTEXT,
  author_ip VARCHAR(255),
  author_user_id BIGINT,
  content LONGTEXT,
  created_at DATETIME,
  created_at_gmt DATETIME,
  status VARCHAR(32),
  `type` VARCHAR(32),
  INDEX idx_comments_article_status_created (article_id, status, created_at_gmt),
  INDEX idx_comments_wp_post_id (wp_post_id),
  INDEX idx_comments_parent_id (parent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
