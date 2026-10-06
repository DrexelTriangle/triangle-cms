CREATE TABLE IF NOT EXISTS newsletter_campaign_lists (
  campaign_id BIGINT UNSIGNED NOT NULL,
  list_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (campaign_id, list_id),
  INDEX idx_newsletter_campaign_lists_list (list_id),
  CONSTRAINT fk_newsletter_campaign_lists_campaign
    FOREIGN KEY (campaign_id) REFERENCES newsletter_campaigns (id) ON DELETE CASCADE,
  CONSTRAINT fk_newsletter_campaign_lists_list
    FOREIGN KEY (list_id) REFERENCES newsletter_lists (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
