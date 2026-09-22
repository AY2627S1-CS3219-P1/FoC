-- +goose Up
-- Campus buildings/regions (COM2, PGP, UTown, ...). S1.2 filter / S1.3 sort key.
-- Coverage is a centre point plus radius; a future polygon migration can
-- replace these two columns without touching the locations table API.
CREATE TABLE buildings (
    id          UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT                 UNIQUE NOT NULL CHECK (char_length(name) > 0),
    center      GEOGRAPHY(Point, 4326) NOT NULL,
    radius_m    DOUBLE PRECISION     NOT NULL CHECK (radius_m > 0),
    created_at  TIMESTAMPTZ          NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ          NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX buildings_center_gix ON buildings USING GIST (center);

-- Reuses UPDATE_TIMESTAMP_FUNC() from 00001_create_users_table.sql.
CREATE TRIGGER set_updated_at_buildings
BEFORE UPDATE ON buildings
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();

-- +goose Down
DROP TRIGGER IF EXISTS set_updated_at_buildings ON buildings;
DROP TABLE IF EXISTS buildings;
