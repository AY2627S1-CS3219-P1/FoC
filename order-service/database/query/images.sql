-- name: InsertItemImage :exec
INSERT INTO order_item_images(id,owner_id,object_key,content_type,size_bytes,created_at,upload_expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: GetItemImage :one
SELECT * FROM order_item_images WHERE id=$1 AND owner_id=$2;

-- name: AttachItemImage :execrows
UPDATE order_item_images SET state='attached',order_id=$3,ordinal=$4
WHERE id=$1 AND owner_id=$2 AND state='uploading' AND object_key=$5 AND size_bytes=$6 AND content_type=$7;

-- name: OrderItemImages :many
SELECT * FROM order_item_images WHERE order_id=$1 ORDER BY ordinal;

-- name: ClaimOrphanImages :many
UPDATE order_item_images target SET state='deleting'
WHERE target.id IN (SELECT candidate.id FROM order_item_images candidate WHERE candidate.state <> 'attached' AND candidate.created_at <= sqlc.arg(created_before) AND candidate.upload_expires_at <= sqlc.arg(now_at) ORDER BY candidate.created_at,candidate.id LIMIT 50 FOR UPDATE OF candidate SKIP LOCKED)
RETURNING target.*;

-- name: DeleteOrphanImage :exec
DELETE FROM order_item_images WHERE id=$1 AND state='deleting';
