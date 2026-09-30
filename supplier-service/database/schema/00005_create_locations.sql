-- +goose Up
-- Campus locations. S1.5: a supplier is a location that can supply items,
-- distinguished by is_supplier. Suppliers have one or more categories through
-- location_categories; ordinary locations have none.
-- details is free-text display info: location instructions, contact info,
-- remote-purchase info and opening hours (S1.4, S1.7, S2.2).
-- archived locations (S2.1.5/6) cannot be selected for new requests.
CREATE TABLE locations (
    id           UUID                   PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT                   NOT NULL CHECK (char_length(name) > 0),
    is_supplier  BOOLEAN                NOT NULL DEFAULT FALSE,
    building_id  UUID                   NOT NULL REFERENCES buildings (id) ON DELETE RESTRICT,
    floor        TEXT,
    coordinates  GEOGRAPHY(Point, 4326) NOT NULL,
    open_from    TIME,
    open_to      TIME,
    contact      TEXT,
    details      TEXT                   NOT NULL DEFAULT '',
    archived_at  TIMESTAMPTZ,
    revision     BIGINT                 NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT locations_hours_pair_check CHECK ((open_from IS NULL) = (open_to IS NULL)),
    CONSTRAINT locations_floor_length_check CHECK (
        floor IS NULL OR char_length(btrim(floor)) BETWEEN 1 AND 50
    ),
    CONSTRAINT locations_revision_positive_check CHECK (revision > 0)
);

CREATE TABLE location_categories (
    location_id UUID        NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    category_id UUID        NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (location_id, category_id)
);

CREATE INDEX locations_coordinates_gix ON locations USING GIST (coordinates);
CREATE INDEX locations_building_idx ON locations (building_id);
CREATE INDEX locations_active_idx ON locations (building_id) WHERE archived_at IS NULL;
CREATE INDEX locations_active_supplier_idx
    ON locations (is_supplier, name, id)
    WHERE archived_at IS NULL;
CREATE INDEX location_categories_category_idx
    ON location_categories (category_id, location_id);
-- S1.1 keyword search over name and details.
CREATE INDEX locations_search_trgm ON locations USING GIN (name gin_trgm_ops, details gin_trgm_ops);

-- Reuses UPDATE_TIMESTAMP_FUNC() from 00001_create_users_table.sql.
CREATE TRIGGER set_updated_at_locations
BEFORE UPDATE ON locations
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();

-- +goose Down
DROP TRIGGER IF EXISTS set_updated_at_locations ON locations;
DROP TABLE IF EXISTS location_categories;
DROP TABLE IF EXISTS locations;
