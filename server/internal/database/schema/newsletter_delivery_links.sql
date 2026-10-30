CREATE TABLE IF NOT EXISTS newsletter_delivery_links (
  delivery_id BIGINT UNSIGNED NOT NULL,
  link_id BIGINT UNSIGNED NOT NULL,
  click_count INT UNSIGNED NOT NULL DEFAULT 0,
  first_clicked_at DATETIME NOT NULL,
  last_clicked_at DATETIME NOT NULL,
  PRIMARY KEY (delivery_id, link_id),
  INDEX idx_newsletter_delivery_links_link (link_id),
  CONSTRAINT fk_newsletter_delivery_links_delivery FOREIGN KEY (delivery_id) REFERENCES newsletter_deliveries (id) ON DELETE CASCADE,
  CONSTRAINT fk_newsletter_delivery_links_link FOREIGN KEY (link_id) REFERENCES newsletter_links (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
