-- The hosted request ID is distinct from the eventual bank transaction ID.
ALTER TABLE payments ADD COLUMN provider_request_id VARCHAR(100) NOT NULL DEFAULT '';
