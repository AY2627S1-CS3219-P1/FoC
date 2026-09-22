-- +goose Up
-- S2.4: user-submitted supplier addition requests. Separate from locations so
-- proposals may be incomplete and rejected history is preserved.
-- Approval (S2.4.1) must, in one transaction: lock the pending row, insert
-- into locations, then mark approved with resulting_location_id.
-- Rejection (S2.4.2) keeps the row for audit. Admin pre-approval edits
-- (S2.4.3) update the proposal row in place while status is pending.
CREATE TABLE supplier_addition_requests (
    id                     UUID                   PRIMARY KEY DEFAULT gen_random_uuid(),
    submitted_by           TEXT                   NOT NULL,
    name                   TEXT                   NOT NULL CHECK (char_length(name) > 0),
    category_id            UUID                   REFERENCES categories (id) ON DELETE SET NULL,
    building_id            UUID                   REFERENCES buildings (id) ON DELETE SET NULL,
    coordinates            GEOGRAPHY(Point, 4326),
    details                TEXT                   NOT NULL DEFAULT '',
    status                 TEXT                   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by            TEXT,
    reviewed_at            TIMESTAMPTZ,
    review_note            TEXT,
    resulting_location_id  UUID                   UNIQUE REFERENCES locations (id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((status = 'pending') = (reviewed_by IS NULL AND reviewed_at IS NULL)),
    CHECK ((status = 'approved') = (resulting_location_id IS NOT NULL))
);

CREATE INDEX supplier_addition_requests_status_idx ON supplier_addition_requests (status);

-- Reuses UPDATE_TIMESTAMP_FUNC() from 00001_create_users_table.sql.
CREATE TRIGGER set_updated_at_supplier_addition_requests
BEFORE UPDATE ON supplier_addition_requests
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();

-- +goose Down
DROP TRIGGER IF EXISTS set_updated_at_supplier_addition_requests ON supplier_addition_requests;
DROP TABLE IF EXISTS supplier_addition_requests;
