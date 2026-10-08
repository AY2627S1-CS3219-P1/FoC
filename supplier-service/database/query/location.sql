-- name: GetLocation :one
SELECT
    l.id,
    l.name,
    l.is_supplier,
    l.floor,
    ST_Y(l.coordinates::GEOMETRY)::DOUBLE PRECISION AS latitude,
    ST_X(l.coordinates::GEOMETRY)::DOUBLE PRECISION AS longitude,
    l.open_from,
    l.open_to,
    l.contact,
    l.details,
    l.archived_at,
    l.revision,
    l.created_at,
    l.updated_at,
    b.id AS building_id,
    b.name AS building_name,
    ST_Y(b.center::GEOMETRY)::DOUBLE PRECISION AS building_latitude,
    ST_X(b.center::GEOMETRY)::DOUBLE PRECISION AS building_longitude,
    b.radius_m AS building_radius_m
FROM locations l
JOIN buildings b ON b.id = l.building_id
WHERE l.id = @id;

-- name: ListLocations :many
-- Filters match CountLocations. search is a pre-escaped ILIKE pattern body;
-- word similarity catches typos. Ties sort by id so pages are stable.
SELECT
    l.id,
    l.name,
    l.is_supplier,
    l.floor,
    ST_Y(l.coordinates::GEOMETRY)::DOUBLE PRECISION AS latitude,
    ST_X(l.coordinates::GEOMETRY)::DOUBLE PRECISION AS longitude,
    l.open_from,
    l.open_to,
    l.contact,
    l.details,
    l.archived_at,
    l.revision,
    l.created_at,
    l.updated_at,
    b.id AS building_id,
    b.name AS building_name,
    ST_Y(b.center::GEOMETRY)::DOUBLE PRECISION AS building_latitude,
    ST_X(b.center::GEOMETRY)::DOUBLE PRECISION AS building_longitude,
    b.radius_m AS building_radius_m
FROM locations l
JOIN buildings b ON b.id = l.building_id
WHERE (@search::TEXT = ''
        OR l.name ILIKE '%' || @search::TEXT || '%'
        OR @raw_search::TEXT <% l.name)
    AND (sqlc.narg(building_id)::UUID IS NULL OR l.building_id = sqlc.narg(building_id)::UUID)
    AND (sqlc.narg(category_id)::UUID IS NULL OR EXISTS (
        SELECT 1 FROM location_categories lc
        WHERE lc.location_id = l.id AND lc.category_id = sqlc.narg(category_id)::UUID))
    AND (NOT @suppliers_only::BOOLEAN OR l.is_supplier)
    AND (@archive_filter::TEXT = 'all'
        OR (@archive_filter::TEXT = 'active' AND l.archived_at IS NULL)
        OR (@archive_filter::TEXT = 'archived' AND l.archived_at IS NOT NULL))
ORDER BY
    CASE WHEN @sort_field::TEXT = 'building' AND NOT @descending::BOOLEAN THEN lower(b.name) END ASC,
    CASE WHEN @sort_field::TEXT = 'building' AND @descending::BOOLEAN THEN lower(b.name) END DESC,
    CASE WHEN NOT @descending::BOOLEAN THEN lower(l.name) END ASC,
    CASE WHEN @descending::BOOLEAN THEN lower(l.name) END DESC,
    CASE WHEN NOT @descending::BOOLEAN THEN l.id END ASC,
    CASE WHEN @descending::BOOLEAN THEN l.id END DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountLocations :one
SELECT count(*)
FROM locations l
WHERE (@search::TEXT = ''
        OR l.name ILIKE '%' || @search::TEXT || '%'
        OR @raw_search::TEXT <% l.name)
    AND (sqlc.narg(building_id)::UUID IS NULL OR l.building_id = sqlc.narg(building_id)::UUID)
    AND (sqlc.narg(category_id)::UUID IS NULL OR EXISTS (
        SELECT 1 FROM location_categories lc
        WHERE lc.location_id = l.id AND lc.category_id = sqlc.narg(category_id)::UUID))
    AND (NOT @suppliers_only::BOOLEAN OR l.is_supplier)
    AND (@archive_filter::TEXT = 'all'
        OR (@archive_filter::TEXT = 'active' AND l.archived_at IS NULL)
        OR (@archive_filter::TEXT = 'archived' AND l.archived_at IS NOT NULL));

-- name: ListLocationCategories :many
SELECT lc.location_id, c.id, c.name
FROM location_categories lc
JOIN categories c ON c.id = lc.category_id
WHERE lc.location_id = ANY(@location_ids::UUID[])
ORDER BY lc.location_id, lower(c.name), c.id;

-- name: ListCurrentDisablements :many
-- At most one per Location: the most recently started active interval, then
-- the most recently created, then id so ties resolve the same way every time.
SELECT DISTINCT ON (d.location_id)
    d.location_id,
    d.id,
    d.starts_at,
    d.ends_at,
    d.reason
FROM location_disablements d
WHERE d.location_id = ANY(@location_ids::UUID[])
    AND d.cancelled_at IS NULL
    AND d.ended_at IS NULL
    AND d.starts_at <= now()
    AND (d.ends_at IS NULL OR d.ends_at > now())
ORDER BY d.location_id, d.starts_at DESC, d.created_at DESC, d.id DESC;

-- name: ListBuildings :many
SELECT
    id,
    name,
    ST_Y(center::GEOMETRY)::DOUBLE PRECISION AS latitude,
    ST_X(center::GEOMETRY)::DOUBLE PRECISION AS longitude,
    radius_m
FROM buildings
ORDER BY lower(name), id;

-- name: ListCategories :many
SELECT id, name
FROM categories
ORDER BY lower(name), id;

-- Lock only the parent. Relationship reads must use a later statement after
-- any concurrent holder of this lock commits.
-- name: LockLocation :one
SELECT id FROM locations WHERE id = @id FOR UPDATE;

-- name: BuildingExists :one
SELECT EXISTS(SELECT 1 FROM buildings WHERE id = @id) AS present;

-- name: ExistingCategoryIDs :many
SELECT id FROM categories WHERE id = ANY(@ids::UUID[]);

-- name: InsertLocation :exec
INSERT INTO locations (
    id, name, is_supplier, building_id, floor, coordinates,
    open_from, open_to, contact, details, revision, created_at
) VALUES (
    @id, @name, @is_supplier, @building_id, @floor,
    ST_SetSRID(ST_MakePoint(@longitude::DOUBLE PRECISION, @latitude::DOUBLE PRECISION), 4326)::geography,
    @open_from, @open_to, @contact, @details, 1, @created_at
);

-- name: UpdateLocation :execrows
UPDATE locations SET
    name = @name,
    is_supplier = @is_supplier,
    building_id = @building_id,
    floor = @floor,
    coordinates = ST_SetSRID(ST_MakePoint(@longitude::DOUBLE PRECISION, @latitude::DOUBLE PRECISION), 4326)::geography,
    open_from = @open_from,
    open_to = @open_to,
    contact = @contact,
    details = @details,
    revision = revision + 1
WHERE id = @id AND revision = @expected_revision;

-- name: SetLocationArchived :execrows
UPDATE locations SET archived_at = @archived_at, revision = revision + 1
WHERE id = @id AND revision = @expected_revision;

-- name: DeleteLocationCategories :exec
DELETE FROM location_categories WHERE location_id = @location_id;

-- name: InsertLocationCategory :exec
INSERT INTO location_categories (location_id, category_id)
VALUES (@location_id, @category_id);
