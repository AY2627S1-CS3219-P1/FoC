-- +goose Up
-- Profile pictures use the same row-as-reference model as review photos.
-- A row is created pending when its PUT grant is issued, becomes active when
-- the caller submits it, and becomes deleting when it is replaced or removed.
-- users.profile_picture_id points at the active row; the storage key stays an
-- attribute of the row and is never the application-level reference.
CREATE TABLE profile_pictures (
    id           UUID        PRIMARY KEY,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    object_key   TEXT        NOT NULL UNIQUE,
    content_type TEXT        NOT NULL CHECK (content_type IN (
                                 'image/jpeg', 'image/png', 'image/webp',
                                 'image/heic', 'image/heif'
                             )),
    size_bytes   BIGINT      NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
    state        TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (state IN ('pending', 'active', 'deleting')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one active picture per user.
CREATE UNIQUE INDEX profile_pictures_one_active
    ON profile_pictures (user_id) WHERE state = 'active';

-- Supports user deletion cascades for pending/deleting rows.
CREATE INDEX profile_pictures_user ON profile_pictures (user_id);

CREATE INDEX profile_pictures_cleanup
    ON profile_pictures (created_at) WHERE state <> 'active';

ALTER TABLE users
    ADD COLUMN profile_picture_id UUID REFERENCES profile_pictures (id) ON DELETE SET NULL;

-- Row deletes fire the SET NULL check; keep cleanup from scanning users.
CREATE INDEX users_profile_picture_id
    ON users (profile_picture_id) WHERE profile_picture_id IS NOT NULL;

ALTER TABLE users DROP COLUMN profile_picture_key;

-- +goose Down
ALTER TABLE users ADD COLUMN profile_picture_key TEXT;
ALTER TABLE users DROP COLUMN profile_picture_id;
DROP TABLE profile_pictures;
