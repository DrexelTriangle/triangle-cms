CREATE TABLE IF NOT EXISTS cms_quiz_answer_counts (
  quiz_id BIGINT NOT NULL,
  definition_version INT NOT NULL,
  question_index INT NOT NULL,
  answer_index INT NOT NULL,
  answer_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (quiz_id, definition_version, question_index, answer_index),
  CONSTRAINT fk_cms_quiz_answer_counts_quiz
    FOREIGN KEY (quiz_id) REFERENCES cms_quizzes (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
