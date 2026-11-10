-- +goose Up
ALTER TABLE profile_pictures ADD COLUMN delete_retry_at TIMESTAMPTZ;

CREATE INDEX profile_pictures_deleting_retry
    ON profile_pictures (delete_retry_at, created_at, id)
    WHERE state = 'deleting';

-- +goose Down
DROP INDEX profile_pictures_deleting_retry;
ALTER TABLE profile_pictures DROP COLUMN delete_retry_at;
