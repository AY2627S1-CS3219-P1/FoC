-- +goose Up
-- Extend idx_warnings_user with id DESC so the warning list queries' full
-- ORDER BY (created_at DESC, id DESC) is index-served and paginated reads
-- avoid an incremental sort before LIMIT.
DROP INDEX IF EXISTS idx_warnings_user;
CREATE INDEX idx_warnings_user ON account_warnings (user_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_warnings_user;
CREATE INDEX idx_warnings_user ON account_warnings (user_id, created_at DESC);
