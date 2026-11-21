-- AI features: a log of every model call (for cost control and accountability) and the
-- latest AI pre-check of each event, so reviewers don't pay for the same check twice.

CREATE TABLE ai_usage (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  feature VARCHAR(40) NOT NULL,
  user_id BIGINT UNSIGNED NULL,
  model VARCHAR(80) NOT NULL,
  prompt_tokens INT UNSIGNED NOT NULL DEFAULT 0,
  cached_tokens INT UNSIGNED NOT NULL DEFAULT 0,
  completion_tokens INT UNSIGNED NOT NULL DEFAULT 0,
  status ENUM('ok','failed','rejected') NOT NULL,
  latency_ms INT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_ai_usage_created (created_at),
  KEY idx_ai_usage_feature_created (feature, created_at),
  CONSTRAINT fk_ai_usage_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE ai_event_reviews (
  event_id BIGINT UNSIGNED NOT NULL,
  result JSON NOT NULL,
  model VARCHAR(80) NOT NULL,
  -- A hash of what was checked (event details and ticket types), so reviewers can see when
  -- the event has changed since.
  input_hash CHAR(64) NOT NULL,
  requested_by BIGINT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (event_id),
  CONSTRAINT fk_ai_event_reviews_event FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_event_reviews_user FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
