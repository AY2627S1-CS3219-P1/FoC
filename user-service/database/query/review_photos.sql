-- Review photo persistence. Staging rows are created by the upload RPC;
-- attachment happens inside the review submission transaction, guarded so a
-- photo claimed by cleanup or attached by a racing request fails the update.

-- name: InsertReviewPhoto :exec
INSERT INTO review_photos (
    id, owner_id, object_key, content_type, size_bytes, state, created_at, upload_expires_at
)
VALUES (
    @id::UUID, @owner_id::UUID, @object_key, @content_type, @size_bytes,
    'uploading', now(), @upload_expires_at::TIMESTAMPTZ
);

-- The owner check makes the staged photo attachable only by its uploader.
-- name: GetReviewPhoto :one
SELECT id, owner_id, object_key, content_type, size_bytes, state, review_id, ordinal, created_at, upload_expires_at
FROM review_photos
WHERE id = @id::UUID AND owner_id = @owner_id::UUID;

-- The guarded predicate mirrors the inspected metadata: a photo whose stored
-- object was deleted, replaced or claimed between inspection and attach
-- matches zero rows and fails the submission precondition.
-- name: AttachReviewPhoto :execrows
UPDATE review_photos
SET state = 'attached', review_id = @review_id::UUID, ordinal = @ordinal
WHERE id = @id::UUID AND owner_id = @owner_id::UUID AND state = 'uploading'
  AND object_key = @object_key AND content_type = @content_type AND size_bytes = @size_bytes;

-- Photos for a page of reviews, in display order. object_key feeds the
-- download grant; the URL itself is never persisted.
-- name: ListReviewPhotosForReviews :many
SELECT id, review_id, object_key, content_type, ordinal
FROM review_photos
WHERE review_id = ANY (@review_ids::UUID[]) AND state = 'attached'
ORDER BY review_id, ordinal;

-- Claims expire orphan staging rows in bulk. SKIP LOCKED keeps concurrent
-- cleanup replicas from fighting over the same rows.
-- name: ClaimOrphanReviewPhotos :many
UPDATE review_photos target
SET state = 'deleting'
WHERE target.id IN (
    SELECT candidate.id
    FROM review_photos candidate
    WHERE candidate.state <> 'attached'
      AND candidate.created_at <= @created_before::TIMESTAMPTZ
      AND candidate.upload_expires_at <= @now_at::TIMESTAMPTZ
    ORDER BY candidate.created_at, candidate.id
    LIMIT 50
    FOR UPDATE OF candidate SKIP LOCKED
)
RETURNING target.id, target.object_key;

-- Deletion completes only after the stored object is gone; retryable.
-- name: DeleteOrphanReviewPhoto :execrows
DELETE FROM review_photos WHERE id = @id::UUID AND state = 'deleting';
