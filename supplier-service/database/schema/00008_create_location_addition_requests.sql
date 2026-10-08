-- +goose Up
-- Requests remain separate from Locations so rejected and withdrawn proposals
-- retain their history. Approval creates a Location and completes its request
-- in one transaction. Application writes supply audit time from the injected clock.
CREATE TABLE location_addition_requests (
    id                     UUID                   PRIMARY KEY DEFAULT gen_random_uuid(),
    submitted_by           TEXT                   NOT NULL,
    name                   TEXT                   NOT NULL CHECK (char_length(name) > 0),
    is_supplier            BOOLEAN                NOT NULL DEFAULT FALSE,
    category_id            UUID                   REFERENCES categories (id) ON DELETE SET NULL,
    building_id            UUID                   REFERENCES buildings (id) ON DELETE SET NULL,
    coordinates            GEOGRAPHY(Point, 4326),
    open_from              TIME,
    open_to                TIME,
    contact                TEXT,
    details                TEXT                   NOT NULL DEFAULT '',
    status                 TEXT                   NOT NULL DEFAULT 'pending',
    reviewed_by            TEXT,
    reviewed_at            TIMESTAMPTZ,
    review_note            TEXT,
    resulting_location_id  UUID                   UNIQUE REFERENCES locations (id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    floor                  TEXT,
    revision               BIGINT NOT NULL DEFAULT 1,
    CHECK ((open_from IS NULL) = (open_to IS NULL)),
    CONSTRAINT requests_revision_check CHECK (revision > 0),
    CONSTRAINT requests_status_check CHECK (status IN ('pending', 'approved', 'rejected', 'withdrawn')),
    CONSTRAINT requests_review_check CHECK (
        (status IN ('pending', 'withdrawn') AND reviewed_by IS NULL AND reviewed_at IS NULL AND review_note IS NULL AND resulting_location_id IS NULL)
        OR (status = 'approved' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND resulting_location_id IS NOT NULL)
        OR (status = 'rejected' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND resulting_location_id IS NULL AND char_length(btrim(review_note)) BETWEEN 1 AND 2000 AND review_note IS NOT NULL)
    ),
    CONSTRAINT requests_proposal_check CHECK (
        status IN ('withdrawn', 'rejected') OR (
        char_length(btrim(name)) BETWEEN 1 AND 200
        AND (floor IS NULL OR char_length(btrim(floor)) BETWEEN 1 AND 50)
        AND (contact IS NULL OR char_length(btrim(contact)) <= 500)
        AND char_length(btrim(details)) <= 2000
        AND building_id IS NOT NULL AND coordinates IS NOT NULL
        AND (open_from IS NULL OR open_from <> open_to)
        AND ST_Y(coordinates::geometry) BETWEEN -90 AND 90
        AND ST_X(coordinates::geometry) BETWEEN -180 AND 180)
    )
);

CREATE TABLE location_addition_request_categories (
    request_id UUID NOT NULL REFERENCES location_addition_requests (id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (request_id, category_id)
);
CREATE INDEX request_categories_category_idx ON location_addition_request_categories (category_id, request_id);
CREATE INDEX location_addition_requests_status_idx ON location_addition_requests (status);
CREATE INDEX requests_list_idx ON location_addition_requests (created_at DESC, id);
CREATE INDEX requests_owner_list_idx ON location_addition_requests (submitted_by, created_at DESC, id);
CREATE INDEX requests_status_list_idx ON location_addition_requests (status, created_at DESC, id);

-- +goose Down
DROP TABLE IF EXISTS location_addition_request_categories;
DROP TABLE IF EXISTS location_addition_requests;
