CREATE TABLE IF NOT EXISTS newsletter_subscriber_lists (
  subscriber_id BIGINT UNSIGNED NOT NULL,
  list_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (subscriber_id, list_id),
  INDEX idx_newsletter_subscriber_lists_list (list_id),
  CONSTRAINT fk_newsletter_subscriber_lists_subscriber
    FOREIGN KEY (subscriber_id) REFERENCES newsletter_subscribers (id) ON DELETE CASCADE,
  CONSTRAINT fk_newsletter_subscriber_lists_list
    FOREIGN KEY (list_id) REFERENCES newsletter_lists (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
