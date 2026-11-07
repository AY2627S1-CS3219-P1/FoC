-- +goose Up
-- Review photos: zero to five images per review. Rows are created as staging
-- while the reviewer composes, then promoted (state 'attached', review_id and
-- ordinal set) inside the submission transaction. content_type and size_bytes
-- record what the upload grant signed; attachment re-checks them against the
-- stored object, closing the gap between upload and submit.
-- Photos are soft-deleted with their review: reviews are never hard-deleted,
-- so no ON DELETE action is needed on review_id.
CREATE TABLE review_photos (
    id                UUID        PRIMARY KEY,
    owner_id          UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    object_key        TEXT        NOT NULL UNIQUE,
    content_type      TEXT        NOT NULL CHECK (content_type IN (
                          'image/jpeg', 'image/png', 'image/webp',
                          'image/heic', 'image/heif'
                      )),
    size_bytes        BIGINT      NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
    state             TEXT        NOT NULL DEFAULT 'uploading'
                      CHECK (state IN ('uploading', 'attached', 'deleting')),
    review_id         UUID        REFERENCES reviews (id),
    ordinal           INTEGER     CHECK (ordinal BETWEEN 0 AND 4),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    upload_expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (review_id, ordinal),
    CHECK ((state = 'attached') = (review_id IS NOT NULL)),
    CHECK ((review_id IS NOT NULL) = (ordinal IS NOT NULL))
);

CREATE INDEX review_photos_owner ON review_photos (owner_id);
CREATE INDEX review_photos_cleanup ON review_photos (created_at) WHERE state <> 'attached';

-- +goose Down
DROP TABLE review_photos;
