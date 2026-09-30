-- name: LockWorkflowLocation :one
SELECT l.*, ST_Y(l.coordinates::geometry)::float8 AS latitude,
    ST_X(l.coordinates::geometry)::float8 AS longitude,
    b.name AS building_name, ST_Y(b.center::geometry)::float8 AS building_latitude,
    ST_X(b.center::geometry)::float8 AS building_longitude, b.radius_m,
    b.created_at AS building_created_at, b.updated_at AS building_updated_at
FROM locations l JOIN buildings b ON b.id=l.building_id WHERE l.id=$1 FOR UPDATE OF l;

-- name: WorkflowLocationCategories :many
SELECT c.* FROM categories c JOIN location_categories lc ON lc.category_id=c.id
WHERE lc.location_id=$1 ORDER BY c.id;

-- name: LockWorkflowDisablement :one
SELECT * FROM location_disablements WHERE id=$1 FOR UPDATE;

-- name: InsertWorkflowDisablement :exec
INSERT INTO location_disablements (id,location_id,starts_at,ends_at,reason,created_by,revision,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: UpdateWorkflowDisablement :execrows
UPDATE location_disablements SET starts_at=$2, ends_at=$3, ended_at=$4, cancelled_at=$5, reason=$6,
    revision=$7, updated_at=$8 WHERE id=$1 AND revision=sqlc.arg(expected_revision);

-- name: WorkflowDisablementOverlap :one
SELECT EXISTS(SELECT 1 FROM location_disablements
WHERE location_id=$1 AND id<>$2 AND cancelled_at IS NULL
    AND tstzrange(starts_at, LEAST(ends_at,ended_at), '[)') && tstzrange(sqlc.arg(starts_at)::timestamptz,sqlc.narg(ends_at)::timestamptz,'[)'));

-- name: ListWorkflowDisablements :many
SELECT * FROM location_disablements WHERE location_id=$1 AND (
    sqlc.arg(state)::text='' OR sqlc.arg(state)::text=CASE
    WHEN cancelled_at IS NOT NULL THEN 'cancelled'
    WHEN ended_at IS NOT NULL OR ends_at <= sqlc.arg(now_at)::timestamptz THEN 'ended'
    WHEN starts_at > sqlc.arg(now_at)::timestamptz THEN 'scheduled' ELSE 'active' END
) ORDER BY starts_at DESC,id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset)::bigint;

-- name: CountWorkflowDisablements :one
SELECT count(*) FROM location_disablements WHERE location_id=$1 AND (
    sqlc.arg(state)::text='' OR sqlc.arg(state)::text=CASE
    WHEN cancelled_at IS NOT NULL THEN 'cancelled'
    WHEN ended_at IS NOT NULL OR ends_at <= sqlc.arg(now_at)::timestamptz THEN 'ended'
    WHEN starts_at > sqlc.arg(now_at)::timestamptz THEN 'scheduled' ELSE 'active' END
);

-- name: LockWorkflowRequest :one
SELECT r.*, COALESCE(ST_Y(r.coordinates::geometry),0)::float8 AS latitude,
    COALESCE(ST_X(r.coordinates::geometry),0)::float8 AS longitude,
    ARRAY[]::uuid[] AS category_ids
FROM location_addition_requests r WHERE r.id=$1 FOR UPDATE;

-- Fetch relationships only after LockWorkflowRequest completes. A subquery in
-- the locking SELECT can retain the pre-wait statement snapshot.
-- name: WorkflowRequestCategoryIDs :many
SELECT category_id FROM location_addition_request_categories WHERE request_id=$1 ORDER BY category_id;

-- name: ListWorkflowRequests :many
SELECT r.*, COALESCE(ST_Y(r.coordinates::geometry),0)::float8 AS latitude,
    COALESCE(ST_X(r.coordinates::geometry),0)::float8 AS longitude,
    ARRAY(SELECT category_id FROM location_addition_request_categories WHERE request_id=r.id ORDER BY category_id)::uuid[] AS category_ids
FROM location_addition_requests r
WHERE (sqlc.arg(is_admin)::boolean OR submitted_by=sqlc.arg(caller_id)::text OR status='approved')
    AND (sqlc.arg(status_filter)::text='' OR status=sqlc.arg(status_filter)::text)
ORDER BY created_at DESC,id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset)::bigint;

-- name: CountWorkflowRequests :one
SELECT count(*) FROM location_addition_requests
WHERE (sqlc.arg(is_admin)::boolean OR submitted_by=sqlc.arg(caller_id)::text OR status='approved')
    AND (sqlc.arg(status_filter)::text='' OR status=sqlc.arg(status_filter)::text);

-- name: InsertWorkflowRequest :exec
INSERT INTO location_addition_requests (id,submitted_by,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,status,revision,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,ST_SetSRID(ST_MakePoint(sqlc.arg(longitude)::float8,sqlc.arg(latitude)::float8),4326)::geography,$7,$8,$9,$10,'pending',1,$11,$11);

-- name: UpdateWorkflowRequest :execrows
UPDATE location_addition_requests SET name=$2,is_supplier=$3,building_id=$4,floor=$5,
    coordinates=CASE WHEN sqlc.arg(coordinates_missing)::boolean THEN NULL ELSE ST_SetSRID(ST_MakePoint(sqlc.arg(longitude)::float8,sqlc.arg(latitude)::float8),4326)::geography END,
    open_from=$6,open_to=$7,contact=$8,details=$9,status=$10,reviewed_by=$11,reviewed_at=$12,review_note=$13,
    resulting_location_id=$14,revision=$15,updated_at=$16
WHERE id=$1 AND status='pending' AND revision=sqlc.arg(expected_revision);

-- name: DeleteWorkflowRequestCategories :exec
DELETE FROM location_addition_request_categories WHERE request_id=$1;

-- name: InsertWorkflowRequestCategory :exec
INSERT INTO location_addition_request_categories (request_id,category_id) VALUES ($1,$2);

-- name: WorkflowBuildingExists :one
SELECT EXISTS(SELECT 1 FROM buildings WHERE id=$1);

-- name: WorkflowCategoryExists :one
SELECT EXISTS(SELECT 1 FROM categories WHERE id=$1);

-- name: InsertWorkflowLocation :exec
INSERT INTO locations (id,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,revision,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,ST_SetSRID(ST_MakePoint(sqlc.arg(longitude)::float8,sqlc.arg(latitude)::float8),4326)::geography,$6,$7,$8,$9,1,$10,$10);

-- name: InsertWorkflowLocationCategory :exec
INSERT INTO location_categories (location_id,category_id) VALUES ($1,$2);

-- name: LockWorkflowIdempotency :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(scope)::text,0));

-- name: DeleteExpiredWorkflowIdempotency :exec
DELETE FROM supplier_idempotency WHERE expires_at<=sqlc.arg(now_at)::timestamptz;

-- name: GetWorkflowIdempotency :one
SELECT * FROM supplier_idempotency WHERE caller_id=$1 AND method=$2 AND key=$3;

-- name: InsertWorkflowIdempotency :exec
INSERT INTO supplier_idempotency (caller_id,method,key,request_hash,resource_id,expires_at) VALUES ($1,$2,$3,$4,$5,$6);

-- name: CurrentWorkflowDisablement :one
SELECT * FROM location_disablements WHERE location_id=$1 AND cancelled_at IS NULL AND ended_at IS NULL
AND starts_at<=sqlc.arg(now_at)::timestamptz AND (ends_at IS NULL OR ends_at>sqlc.arg(now_at)::timestamptz)
ORDER BY starts_at DESC,id LIMIT 1;
