-- name: InsertEvidence :exec
INSERT INTO evidence_attachments (
    id, owner_id, object_key, content_type, size_bytes, state, created_at, upload_expires_at
)
VALUES (
    @id::UUID, @owner_id::UUID, @object_key, @content_type, @size_bytes,
    'uploading', now(), @upload_expires_at::TIMESTAMPTZ
);

-- name: GetStagedEvidence :one
SELECT id, object_key, content_type, size_bytes, state
FROM evidence_attachments
WHERE id = @id::UUID AND owner_id = @owner_id::UUID;

-- Zero rows means the image was claimed, deleted or replaced since inspection.
-- name: AttachEvidence :execrows
UPDATE evidence_attachments
SET state = 'attached',
    report_id = sqlc.narg(report_id)::UUID,
    report_response_id = sqlc.narg(report_response_id)::UUID,
    appeal_id = sqlc.narg(appeal_id)::UUID,
    appeal_response_id = sqlc.narg(appeal_response_id)::UUID,
    ordinal = @ordinal
WHERE id = @id::UUID AND owner_id = @owner_id::UUID AND state = 'uploading'
  AND object_key = @object_key AND content_type = @content_type AND size_bytes = @size_bytes;

-- name: ListEvidenceForParents :many
SELECT id, object_key, content_type, report_id, report_response_id, appeal_id, appeal_response_id
FROM evidence_attachments
WHERE state = 'attached'
  AND (report_id = ANY (@report_ids::UUID[])
       OR report_response_id = ANY (@report_response_ids::UUID[])
       OR appeal_id = ANY (@appeal_ids::UUID[])
       OR appeal_response_id = ANY (@appeal_response_ids::UUID[]))
ORDER BY ordinal, id;

-- The report or appeal whose visibility rules govern an attached image, also
-- for images attached to a response. The nil UUID stands for "none".
-- name: LocateEvidence :one
SELECT e.object_key, e.content_type,
       COALESCE(e.report_id, rr.report_id, '00000000-0000-0000-0000-000000000000')::UUID AS owning_report_id,
       COALESCE(e.appeal_id, ar.appeal_id, '00000000-0000-0000-0000-000000000000')::UUID AS owning_appeal_id
FROM evidence_attachments e
LEFT JOIN report_responses rr ON rr.id = e.report_response_id
LEFT JOIN appeal_responses ar ON ar.id = e.appeal_response_id
WHERE e.id = @id::UUID AND e.state = 'attached';

-- SKIP LOCKED lets concurrent replicas claim different rows.
-- name: ClaimOrphanEvidence :many
UPDATE evidence_attachments target
SET state = 'deleting'
WHERE target.id IN (
    SELECT candidate.id
    FROM evidence_attachments candidate
    WHERE candidate.state <> 'attached'
      AND candidate.created_at <= @created_before::TIMESTAMPTZ
      AND candidate.upload_expires_at <= @now_at::TIMESTAMPTZ
    ORDER BY candidate.created_at, candidate.id
    LIMIT 50
    FOR UPDATE OF candidate SKIP LOCKED
)
RETURNING target.id, target.object_key;

-- name: DeleteOrphanEvidence :execrows
DELETE FROM evidence_attachments WHERE id = @id::UUID AND state = 'deleting';
