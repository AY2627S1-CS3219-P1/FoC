-- Submission

-- name: AppealByIdempotencyKey :one
SELECT id, request_hash
FROM appeals
WHERE appellant_id = @appellant_id::UUID AND idempotency_key = @idempotency_key::UUID;

-- A concurrent submission with the same key inserts nothing; the caller then
-- reads the winner with AppealByIdempotencyKey.
-- name: InsertAppeal :execrows
INSERT INTO appeals (id, appellant_id, request_id, reason, idempotency_key, request_hash)
VALUES (
    @id::UUID, @appellant_id::UUID, sqlc.narg(request_id)::UUID, @reason::TEXT,
    @idempotency_key::UUID, @request_hash::TEXT
)
ON CONFLICT (appellant_id, idempotency_key) DO NOTHING;

-- name: InsertDecision :exec
INSERT INTO appeal_decisions (id, appeal_id, kind, report_id, warning_id, review_id, counterpart_id)
VALUES (
    @id::UUID, @appeal_id::UUID, @kind::TEXT, sqlc.narg(report_id)::UUID, sqlc.narg(warning_id)::UUID,
    sqlc.narg(review_id)::UUID, sqlc.narg(counterpart_id)::UUID
);

-- Eligibility

-- name: ReportForAppeal :one
SELECT reporter_id, subject_id, review_id, status, outcome, request_id
FROM reports
WHERE id = @id::UUID;

-- Whether the reporter closed a report on this review with no action, so the
-- review they reported was kept.
-- name: ReviewRetainedByReport :one
SELECT EXISTS (
    SELECT 1 FROM reports
    WHERE review_id = @review_id::UUID AND status = 'closed' AND outcome = 'no_action'
      AND reporter_id = @reporter_id::UUID
)::BOOLEAN AS retained;

-- Reads

-- name: GetAppeal :one
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at
FROM appeals a
WHERE a.id = @id::UUID;

-- Appeals the user filed or is a counterpart of.
-- name: ListAppealsInvolving :many
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at
FROM appeals a
WHERE a.appellant_id = @user_id::UUID
   OR EXISTS (
       SELECT 1 FROM appeal_decisions d WHERE d.appeal_id = a.id AND d.counterpart_id = @user_id::UUID
   )
ORDER BY a.created_at DESC, a.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountAppealsInvolving :one
SELECT COUNT(*)
FROM appeals a
WHERE a.appellant_id = @user_id::UUID
   OR EXISTS (
       SELECT 1 FROM appeal_decisions d WHERE d.appeal_id = a.id AND d.counterpart_id = @user_id::UUID
   );

-- name: ListAppeals :many
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at
FROM appeals a
WHERE (sqlc.narg('status')::TEXT IS NULL OR a.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('appellant_id')::UUID IS NULL OR a.appellant_id = sqlc.narg('appellant_id')::UUID)
ORDER BY a.created_at DESC, a.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountAppeals :one
SELECT COUNT(*)
FROM appeals a
WHERE (sqlc.narg('status')::TEXT IS NULL OR a.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('appellant_id')::UUID IS NULL OR a.appellant_id = sqlc.narg('appellant_id')::UUID);

-- name: ListDecisions :many
SELECT d.id, d.appeal_id, d.kind, d.report_id, d.warning_id, d.review_id, d.counterpart_id,
       d.outcome, d.outcome_reason
FROM appeal_decisions d
WHERE d.appeal_id = ANY(@appeal_ids::UUID[])
ORDER BY d.appeal_id, d.created_at, d.id;

-- name: ListAppealResponses :many
SELECT ar.id, ar.appeal_id, ar.responder_id, ar.body, ar.created_at
FROM appeal_responses ar
WHERE ar.appeal_id = @appeal_id::UUID
ORDER BY ar.created_at, ar.id;

-- Lifecycle

-- name: LockAppeal :one
SELECT appellant_id, status, awaited_user_id
FROM appeals
WHERE id = @id::UUID
FOR UPDATE;

-- Decisions do not change while the appeal row is locked, except through
-- ResolveDecision under that lock. The report columns describe what an
-- overturned report outcome must undo; User Service finds a report's warning
-- by the report's ID.
-- name: LockedDecisions :many
SELECT d.id, d.kind, d.report_id, d.warning_id, d.review_id, d.counterpart_id,
       r.outcome AS report_outcome, r.subject_id AS report_subject_id, r.review_id AS report_review_id
FROM appeal_decisions d
LEFT JOIN reports r ON r.id = d.report_id
WHERE d.appeal_id = @appeal_id::UUID
ORDER BY d.created_at, d.id;

-- name: TransitionAppeal :execrows
UPDATE appeals
SET status = @to_status::TEXT,
    awaited_user_id = sqlc.narg(awaited_user_id)::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status = @from_status::TEXT;

-- name: CloseAppeal :execrows
UPDATE appeals
SET status = 'closed',
    awaited_user_id = NULL,
    closed_at = now(),
    closed_by = @closed_by::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status <> 'closed';

-- name: ResolveDecision :execrows
UPDATE appeal_decisions
SET outcome = @outcome::TEXT,
    outcome_reason = @outcome_reason::TEXT,
    active = false
WHERE id = @id::UUID AND appeal_id = @appeal_id::UUID AND active;

-- name: InsertAppealResponse :exec
INSERT INTO appeal_responses (id, appeal_id, responder_id, body)
VALUES (@id::UUID, @appeal_id::UUID, @responder_id::UUID, @body::TEXT);
