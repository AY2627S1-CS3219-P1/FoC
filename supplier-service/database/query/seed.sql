-- name: GetBuilding :one
SELECT id
FROM buildings
WHERE id = sqlc.arg(id) OR name = sqlc.arg(name)
ORDER BY (id = sqlc.arg(id)) DESC
LIMIT 1;

-- name: UpsertBuilding :execrows
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

-- name: GetCategory :one
SELECT id
FROM categories
WHERE id = sqlc.arg(id) OR name = sqlc.arg(name)
ORDER BY (id = sqlc.arg(id)) DESC
LIMIT 1;

-- name: UpsertCategory :execrows
INSERT INTO categories (id, name)
VALUES (sqlc.arg(id), sqlc.arg(name))
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name
WHERE categories.name IS DISTINCT FROM EXCLUDED.name;

-- name: LocationExists :one
SELECT EXISTS (
    SELECT 1
    FROM locations
    WHERE id = $1
);

-- name: UpsertLocation :execrows
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

-- name: ListLocationCategoryIDs :many
SELECT category_id
FROM location_categories
WHERE location_id = $1
ORDER BY category_id;

-- name: AddLocationCategory :exec
INSERT INTO location_categories (location_id, category_id)
VALUES ($1, $2)
ON CONFLICT (location_id, category_id) DO NOTHING;

-- name: DeleteLocationCategory :exec
DELETE FROM location_categories
WHERE location_id = $1 AND category_id = $2;

-- name: DeleteAllLocationCategories :execrows
DELETE FROM location_categories
WHERE location_id = $1;

-- Demo fixtures use the same sqlc persistence boundary as the CSV importer.
-- name: LockDemoSeed :exec
SELECT pg_advisory_xact_lock(18003219, 2);

-- name: LockDemoLocation :one
SELECT id FROM locations WHERE id=$1 FOR UPDATE;

-- name: SetDemoLocationState :exec
UPDATE locations SET archived_at=$2,
    revision=revision+CASE WHEN sqlc.arg(reset_demo)::boolean THEN 1 ELSE 0 END
WHERE id=$1;

-- name: UpsertDemoRequest :one
INSERT INTO location_addition_requests (
    id,submitted_by,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,
    status,reviewed_by,reviewed_at,review_note,resulting_location_id,revision,created_at,updated_at
)
SELECT sqlc.arg(id),sqlc.arg(submitted_by),l.name,l.is_supplier,l.building_id,l.floor,l.coordinates,
    l.open_from,l.open_to,l.contact,l.details || sqlc.arg(scenario_details)::text,
    sqlc.arg(status),sqlc.narg(reviewed_by),sqlc.narg(reviewed_at),sqlc.narg(review_note),
    sqlc.narg(resulting_location_id),sqlc.arg(revision),sqlc.arg(created_at),sqlc.arg(updated_at)
FROM locations l WHERE l.id=sqlc.arg(proposal_location_id)
ON CONFLICT(id) DO UPDATE SET submitted_by=EXCLUDED.submitted_by,name=EXCLUDED.name,
    is_supplier=EXCLUDED.is_supplier,building_id=EXCLUDED.building_id,floor=EXCLUDED.floor,
    coordinates=EXCLUDED.coordinates,open_from=EXCLUDED.open_from,open_to=EXCLUDED.open_to,
    contact=EXCLUDED.contact,details=EXCLUDED.details,status=EXCLUDED.status,
    reviewed_by=EXCLUDED.reviewed_by,reviewed_at=EXCLUDED.reviewed_at,review_note=EXCLUDED.review_note,
    resulting_location_id=EXCLUDED.resulting_location_id,revision=location_addition_requests.revision+1,
    created_at=EXCLUDED.created_at,updated_at=EXCLUDED.updated_at
WHERE sqlc.arg(reset_demo)::boolean
RETURNING id;

-- name: DeleteDemoRequestCategories :exec
DELETE FROM location_addition_request_categories WHERE request_id=$1;

-- name: CopyDemoRequestCategories :exec
INSERT INTO location_addition_request_categories(request_id,category_id)
SELECT sqlc.arg(request_id),category_id FROM location_categories WHERE location_id=sqlc.arg(location_id);

-- name: DemoDisablementExists :one
SELECT EXISTS(SELECT 1 FROM location_disablements WHERE id=$1);

-- name: DemoDisablementOverlap :one
SELECT EXISTS(SELECT 1 FROM location_disablements
WHERE location_id=$1 AND cancelled_at IS NULL
    AND NOT (id=ANY(sqlc.arg(replacing_ids)::uuid[]))
    AND tstzrange(starts_at, LEAST(ends_at,ended_at), '[)') &&
        tstzrange(sqlc.arg(starts_at)::timestamptz,sqlc.narg(effective_end)::timestamptz,'[)'));

-- name: UpsertDemoDisablement :exec
INSERT INTO location_disablements(id,location_id,starts_at,ends_at,ended_at,cancelled_at,reason,created_by,revision,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1,$9,$10)
ON CONFLICT(id) DO UPDATE SET location_id=EXCLUDED.location_id,starts_at=EXCLUDED.starts_at,
    ends_at=EXCLUDED.ends_at,ended_at=EXCLUDED.ended_at,cancelled_at=EXCLUDED.cancelled_at,
    reason=EXCLUDED.reason,created_by=EXCLUDED.created_by,revision=location_disablements.revision+1,
    created_at=EXCLUDED.created_at,updated_at=EXCLUDED.updated_at
WHERE sqlc.arg(reset_demo)::boolean;
