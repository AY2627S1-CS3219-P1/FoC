-- +goose Up
-- Pending profile-picture uploads. A row is created when a signed PUT grant is
-- issued and removed when the caller submits the upload. The current picture
-- itself lives in users.profile_picture_key; this table only records grants so
-- a submit can prove ownership and abandoned uploads can be reaped.
-- `deleting` claims a row for cleanup, making object deletion retryable and
-- preventing a concurrent submit from applying an object that is being removed.
CREATE TABLE profile_picture_uploads (
    id           UUID        PRIMARY KEY,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    object_key   TEXT        NOT NULL UNIQUE,
    content_type TEXT        NOT NULL CHECK (content_type IN (
                                 'image/jpeg', 'image/png', 'image/webp',
                                 'image/heic', 'image/heif'
                             )),
    size_bytes   BIGINT      NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
    deleting     BOOLEAN     NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unfiltered: cleanup re-claims rows marked deleting so a failed object
-- deletion is retried on the next pass.
CREATE INDEX profile_picture_uploads_cleanup
    ON profile_picture_uploads (created_at);

-- +goose Down
DROP TABLE profile_picture_uploads;
