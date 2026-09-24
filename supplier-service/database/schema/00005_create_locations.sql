-- +goose Up
-- Campus locations. S1.5: a supplier is a location that can supply items,
-- distinguished by is_supplier. Ordinary locations have no category.
-- details is free-text display info: location instructions, contact info,
-- remote-purchase info and opening hours (S1.4, S1.7, S2.2).
-- archived locations (S2.1.5/6) cannot be selected for new requests.
CREATE TABLE locations (
    id           UUID                   PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT                   NOT NULL CHECK (char_length(name) > 0),
    is_supplier  BOOLEAN                NOT NULL DEFAULT FALSE,
    category_id  UUID                   REFERENCES categories (id) ON DELETE SET NULL,
    building_id  UUID                   NOT NULL REFERENCES buildings (id) ON DELETE RESTRICT,
    coordinates  GEOGRAPHY(Point, 4326) NOT NULL,
    open_from    TIMESTAMPTZ            NOT NULL DEFAULT '00:00:00+00',
    open_to      TIMESTAMPTZ            NOT NULL DEFAULT '23:59:59+00',
    contact      TEXT                   NOT NULL DEFAULT '',
    details      TEXT                   NOT NULL DEFAULT '',
    archived_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ            NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX locations_coordinates_gix ON locations USING GIST (coordinates);
CREATE INDEX locations_building_idx ON locations (building_id);
CREATE INDEX locations_category_idx ON locations (category_id);
CREATE INDEX locations_active_idx ON locations (building_id) WHERE archived_at IS NULL;
-- S1.1 keyword search over name and details.
CREATE INDEX locations_search_trgm ON locations USING GIN (name gin_trgm_ops, details gin_trgm_ops);

-- Reuses UPDATE_TIMESTAMP_FUNC() from 00001_create_users_table.sql.
CREATE TRIGGER set_updated_at_locations
BEFORE UPDATE ON locations
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();

-- +goose Down
DROP TRIGGER IF EXISTS set_updated_at_locations ON locations;
DROP TABLE IF EXISTS locations;
