-- +goose Up
ALTER TABLE location_disablements
    ADD COLUMN ended_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT disablements_revision_check CHECK (revision > 0),
    ADD CONSTRAINT disablements_reason_check CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500) NOT VALID,
    ADD CONSTRAINT disablements_terminal_check CHECK (
        NOT (ended_at IS NOT NULL AND cancelled_at IS NOT NULL)
        AND (ended_at IS NULL OR (ended_at >= starts_at AND (ends_at IS NULL OR ended_at <= ends_at)))
        AND (cancelled_at IS NULL OR cancelled_at < starts_at)
    ) NOT VALID;
CREATE INDEX disablements_list_idx ON location_disablements (location_id, starts_at DESC, id);

-- Replace the old contradictory review checks. They made rejected rows
-- impossible and could not represent owner withdrawal. Preserve legacy rows.
-- +goose StatementBegin
DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
        WHERE conrelid = 'location_addition_requests'::regclass AND contype = 'c'
        AND pg_get_constraintdef(oid) ~ '(status|reviewed_by|reviewed_at|resulting_location_id)'
    LOOP
        EXECUTE format('ALTER TABLE location_addition_requests DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;
-- +goose StatementEnd
-- Application writes supply audit time from the injected clock.
DROP TRIGGER set_updated_at_location_addition_requests ON location_addition_requests;
ALTER TABLE location_addition_requests
    ADD COLUMN floor TEXT,
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT requests_revision_check CHECK (revision > 0),
    ADD CONSTRAINT requests_status_check CHECK (status IN ('pending', 'approved', 'rejected', 'withdrawn')),
    ADD CONSTRAINT requests_review_check CHECK (
        (status IN ('pending', 'withdrawn') AND reviewed_by IS NULL AND reviewed_at IS NULL AND review_note IS NULL AND resulting_location_id IS NULL)
        OR (status = 'approved' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND resulting_location_id IS NOT NULL)
        OR (status = 'rejected' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND resulting_location_id IS NULL AND char_length(btrim(review_note)) BETWEEN 1 AND 2000 AND review_note IS NOT NULL)
    ) NOT VALID,
    ADD CONSTRAINT requests_proposal_check CHECK (
        status IN ('withdrawn', 'rejected') OR (
        char_length(btrim(name)) BETWEEN 1 AND 200
        AND (floor IS NULL OR char_length(btrim(floor)) BETWEEN 1 AND 50)
        AND (contact IS NULL OR char_length(btrim(contact)) <= 500)
        AND char_length(btrim(details)) <= 2000
        AND building_id IS NOT NULL AND coordinates IS NOT NULL
        AND (open_from IS NULL OR open_from <> open_to)
        AND ST_Y(coordinates::geometry) BETWEEN -90 AND 90
        AND ST_X(coordinates::geometry) BETWEEN -180 AND 180)
    ) NOT VALID;

CREATE TABLE location_addition_request_categories (
    request_id UUID NOT NULL REFERENCES location_addition_requests (id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (request_id, category_id)
);
-- Retain category_id as a legacy migration source, not a write target.
INSERT INTO location_addition_request_categories (request_id, category_id, created_at)
SELECT id, category_id, created_at FROM location_addition_requests WHERE category_id IS NOT NULL;
CREATE INDEX request_categories_category_idx ON location_addition_request_categories (category_id, request_id);
CREATE INDEX requests_list_idx ON location_addition_requests (created_at DESC, id);
CREATE INDEX requests_owner_list_idx ON location_addition_requests (submitted_by, created_at DESC, id);
CREATE INDEX requests_status_list_idx ON location_addition_requests (status, created_at DESC, id);

CREATE TABLE supplier_idempotency (
    caller_id TEXT NOT NULL,
    method TEXT NOT NULL,
    key UUID NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    resource_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (caller_id, method, key)
);
CREATE INDEX supplier_idempotency_expiry_idx ON supplier_idempotency (expires_at);

-- +goose Down
-- Refuse rollback when the earlier schema cannot retain workflow data.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM location_addition_request_categories GROUP BY request_id HAVING count(*) > 1)
        OR EXISTS (SELECT 1 FROM location_addition_requests WHERE status = 'withdrawn' OR floor IS NOT NULL)
        OR EXISTS (SELECT 1 FROM location_disablements WHERE ended_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot downgrade operational workflows with multi-Category, withdrawn, floor, or early-ended history';
    END IF;
END $$;
-- +goose StatementEnd
-- Legacy proposals may not satisfy the new NOT VALID checks.
ALTER TABLE location_addition_requests
    DROP CONSTRAINT requests_revision_check,
    DROP CONSTRAINT requests_status_check,
    DROP CONSTRAINT requests_review_check,
    DROP CONSTRAINT requests_proposal_check;
UPDATE location_addition_requests SET category_id = NULL;
UPDATE location_addition_requests r SET category_id = c.category_id
FROM location_addition_request_categories c WHERE c.request_id = r.id;
DROP TABLE supplier_idempotency;
DROP TABLE location_addition_request_categories;
DROP INDEX requests_list_idx;
DROP INDEX requests_owner_list_idx;
DROP INDEX requests_status_list_idx;
ALTER TABLE location_addition_requests
    DROP COLUMN floor,
    DROP COLUMN revision,
    ADD CHECK (status IN ('pending', 'approved', 'rejected')),
    ADD CHECK ((status = 'pending') = (reviewed_by IS NULL)) NOT VALID,
    ADD CHECK ((status = 'pending') = (reviewed_at IS NULL)) NOT VALID,
    ADD CHECK ((status = 'approved') = (resulting_location_id IS NOT NULL)) NOT VALID;
CREATE TRIGGER set_updated_at_location_addition_requests
BEFORE UPDATE ON location_addition_requests
FOR EACH ROW EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();
DROP INDEX disablements_list_idx;
ALTER TABLE location_disablements
    DROP CONSTRAINT disablements_revision_check,
    DROP CONSTRAINT disablements_reason_check,
    DROP CONSTRAINT disablements_terminal_check,
    DROP COLUMN ended_at,
    DROP COLUMN updated_at,
    DROP COLUMN revision;
