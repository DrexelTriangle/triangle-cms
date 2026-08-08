CREATE TABLE IF NOT EXISTS article_categories (
  article_id BIGINT NOT NULL,
  -- The category title as it appears in `articles`.`categories`, lowercased and
  -- trimmed. 191 is the longest utf8mb4 VARCHAR that fits a legacy 767-byte
  -- index prefix; the longest category in the corpus is under 30.
  category VARCHAR(191) NOT NULL,
  PRIMARY KEY (article_id, category),
  -- The section-page lookup: given a handful of category titles, find the
  -- articles. article_id rides along so the index covers the whole subquery.
  KEY idx_article_categories_category (category, article_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
