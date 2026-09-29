-- name: GetSeedBuilding :one
SELECT id
FROM buildings
WHERE id = sqlc.arg(id) OR name = sqlc.arg(name)
ORDER BY (id = sqlc.arg(id)) DESC
LIMIT 1;

-- name: UpsertSeedBuilding :execrows
INSERT INTO buildings (id, name, center, radius_m)
VALUES (
    sqlc.arg(id),
    sqlc.arg(name),
    ST_SetSRID(
        ST_MakePoint(
            sqlc.arg(longitude)::DOUBLE PRECISION,
            sqlc.arg(latitude)::DOUBLE PRECISION
        ),
        4326
    )::GEOGRAPHY,
    sqlc.arg(radius_m)
)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    center = EXCLUDED.center,
    radius_m = EXCLUDED.radius_m
WHERE buildings.name IS DISTINCT FROM EXCLUDED.name
   OR NOT ST_Equals(buildings.center::GEOMETRY, EXCLUDED.center::GEOMETRY)
   OR buildings.radius_m IS DISTINCT FROM EXCLUDED.radius_m;

-- name: GetSeedCategory :one
SELECT id
FROM categories
WHERE id = sqlc.arg(id) OR name = sqlc.arg(name)
ORDER BY (id = sqlc.arg(id)) DESC
LIMIT 1;

-- name: UpsertSeedCategory :execrows
INSERT INTO categories (id, name)
VALUES (sqlc.arg(id), sqlc.arg(name))
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name
WHERE categories.name IS DISTINCT FROM EXCLUDED.name;

-- name: SeedLocationExists :one
SELECT EXISTS (
    SELECT 1
    FROM locations
    WHERE id = $1
);

-- name: UpsertSeedLocation :execrows
INSERT INTO locations (
    id,
    name,
    is_supplier,
    building_id,
    floor,
    coordinates,
    open_from,
    open_to,
    contact,
    details
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(name),
    sqlc.arg(is_supplier),
    sqlc.arg(building_id),
    sqlc.narg(floor),
    ST_SetSRID(
        ST_MakePoint(
            sqlc.arg(longitude)::DOUBLE PRECISION,
            sqlc.arg(latitude)::DOUBLE PRECISION
        ),
        4326
    )::GEOGRAPHY,
    sqlc.narg(open_from),
    sqlc.narg(open_to),
    sqlc.narg(contact),
    sqlc.arg(details)
)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    is_supplier = EXCLUDED.is_supplier,
    building_id = EXCLUDED.building_id,
    floor = EXCLUDED.floor,
    coordinates = EXCLUDED.coordinates,
    open_from = EXCLUDED.open_from,
    open_to = EXCLUDED.open_to,
    contact = EXCLUDED.contact,
    details = EXCLUDED.details,
    revision = locations.revision + 1
WHERE locations.name IS DISTINCT FROM EXCLUDED.name
   OR locations.is_supplier IS DISTINCT FROM EXCLUDED.is_supplier
   OR locations.building_id IS DISTINCT FROM EXCLUDED.building_id
   OR locations.floor IS DISTINCT FROM EXCLUDED.floor
   OR NOT ST_Equals(locations.coordinates::GEOMETRY, EXCLUDED.coordinates::GEOMETRY)
   OR locations.open_from IS DISTINCT FROM EXCLUDED.open_from
   OR locations.open_to IS DISTINCT FROM EXCLUDED.open_to
   OR locations.contact IS DISTINCT FROM EXCLUDED.contact
   OR locations.details IS DISTINCT FROM EXCLUDED.details;

-- name: ListSeedLocationCategoryIDs :many
SELECT category_id
FROM location_categories
WHERE location_id = $1
ORDER BY category_id;

-- name: AddSeedLocationCategory :exec
INSERT INTO location_categories (location_id, category_id)
VALUES ($1, $2)
ON CONFLICT (location_id, category_id) DO NOTHING;

-- name: DeleteSeedLocationCategory :exec
DELETE FROM location_categories
WHERE location_id = $1 AND category_id = $2;

-- name: DeleteAllSeedLocationCategories :execrows
DELETE FROM location_categories
WHERE location_id = $1;
