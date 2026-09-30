-- +goose Up
-- New writes satisfy the admin API's scalar limits. NOT VALID retains legacy
-- rows without scanning or rewriting them during deployment.
ALTER TABLE locations
    ADD CONSTRAINT locations_name_admin_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 200) NOT VALID,
    ADD CONSTRAINT locations_contact_admin_check
        CHECK (contact IS NULL OR char_length(btrim(contact)) <= 500) NOT VALID,
    ADD CONSTRAINT locations_details_admin_check
        CHECK (char_length(btrim(details)) <= 2000) NOT VALID,
    ADD CONSTRAINT locations_hours_admin_check
        CHECK (open_from IS NULL OR (
            open_from <> open_to
            AND open_from < TIME '24:00'
            AND open_to < TIME '24:00'
            AND EXTRACT(SECOND FROM open_from) = 0
            AND EXTRACT(SECOND FROM open_to) = 0
        )) NOT VALID,
    ADD CONSTRAINT locations_coordinates_admin_check
        CHECK (
            ST_Y(coordinates::geometry) BETWEEN -90 AND 90
            AND ST_X(coordinates::geometry) BETWEEN -180 AND 180
        ) NOT VALID;

-- +goose Down
ALTER TABLE locations
    DROP CONSTRAINT locations_coordinates_admin_check,
    DROP CONSTRAINT locations_hours_admin_check,
    DROP CONSTRAINT locations_details_admin_check,
    DROP CONSTRAINT locations_contact_admin_check,
    DROP CONSTRAINT locations_name_admin_check;
