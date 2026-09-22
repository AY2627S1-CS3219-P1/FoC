-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
-- postgis ships catalog views/tables that depend on the extension itself,
-- so a plain DROP fails; CASCADE only touches postgis-owned objects here
-- because all geography columns are removed by earlier downs.
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS postgis CASCADE;
