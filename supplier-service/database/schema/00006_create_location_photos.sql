-- +goose Up
-- S1.6: zero or more photos per location.
CREATE TABLE location_photos (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id  UUID        NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    photo_url    TEXT        NOT NULL CHECK (char_length(photo_url) > 0),
    sort_order   INT         NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (location_id, sort_order)
);

CREATE INDEX location_photos_location_idx ON location_photos (location_id);

-- +goose Down
DROP TABLE IF EXISTS location_photos;
