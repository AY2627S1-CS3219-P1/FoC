-- +goose Up
ALTER TABLE locations
    ADD COLUMN floor TEXT,
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;

ALTER TABLE locations
    ADD CONSTRAINT locations_floor_length_check CHECK (
        floor IS NULL OR char_length(btrim(floor)) BETWEEN 1 AND 50
    ),
    ADD CONSTRAINT locations_revision_positive_check CHECK (revision > 0);

CREATE TABLE location_categories (
    location_id UUID NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (location_id, category_id)
);

-- Preserve every legacy relationship. Application operations enforce the
-- Supplier/Category classification invariant for subsequent writes.
INSERT INTO location_categories (location_id, category_id)
SELECT id, category_id
FROM locations
WHERE category_id IS NOT NULL;

CREATE INDEX location_categories_category_idx
    ON location_categories (category_id, location_id);
CREATE INDEX locations_active_supplier_idx
    ON locations (is_supplier, name, id)
    WHERE archived_at IS NULL;

DROP INDEX locations_category_idx;
ALTER TABLE locations DROP COLUMN category_id;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT location_id
        FROM location_categories
        GROUP BY location_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore single-category schema while Locations have multiple Categories';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE locations
    ADD COLUMN category_id UUID REFERENCES categories (id) ON DELETE SET NULL;

UPDATE locations AS location
SET category_id = (
    SELECT category_id
    FROM location_categories
    WHERE location_id = location.id
    ORDER BY category_id
    LIMIT 1
);

CREATE INDEX locations_category_idx ON locations (category_id);

DROP INDEX locations_active_supplier_idx;
DROP TABLE location_categories;

ALTER TABLE locations
    DROP CONSTRAINT locations_revision_positive_check,
    DROP CONSTRAINT locations_floor_length_check,
    DROP COLUMN revision,
    DROP COLUMN floor;
