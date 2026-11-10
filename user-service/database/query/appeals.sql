-- Submission

-- name: AppealByIdempotencyKey :one
SELECT id, request_hash
FROM user_appeals
WHERE appellant_id = @appellant_id::UUID AND idempotency_key = @idempotency_key::UUID;

-- A concurrent submission with the same key inserts nothing; the caller then
-- reads the winner with AppealByIdempotencyKey.
-- name: InsertAppeal :execrows
INSERT INTO user_appeals (id, appellant_id, request_id, reason, idempotency_key, request_hash)
VALUES (
    @id::UUID, @appellant_id::UUID, sqlc.narg(request_id)::UUID, @reason::TEXT,
    @idempotency_key::UUID, @request_hash::TEXT
)
ON CONFLICT (appellant_id, idempotency_key) DO NOTHING;

-- name: InsertDecision :exec
INSERT INTO user_appeal_decisions (id, appeal_id, kind, report_id, warning_id, review_id, counterpart_id)
VALUES (
    @id::UUID, @appeal_id::UUID, @kind::TEXT, sqlc.narg(report_id)::UUID, sqlc.narg(warning_id)::UUID,
    sqlc.narg(review_id)::UUID, sqlc.narg(counterpart_id)::UUID
);

-- Eligibility

-- name: ReportForAppeal :one
SELECT r.reporter_id, r.subject_id, r.review_id, r.status, r.outcome,
       COALESCE(r.order_id, rv.order_id)::UUID AS request_id
FROM user_reports r
LEFT JOIN reviews rv ON rv.id = r.review_id
WHERE r.id = @id::UUID;

-- name: WarningForAppeal :one
SELECT user_id, request_id, report_id, status
FROM account_warnings
WHERE id = @id::UUID AND deleted_at IS NULL;

-- removed_by_report: the latest unrestored removal was a report outcome. A
-- report closes in the same transaction that removes the review, so both rows
-- carry that transaction's now().
-- retained_by_report: the reporter closed a report on this review with no
-- action.
-- name: ReviewForAppeal :one
SELECT r.reviewer_id, r.reviewee_id, r.order_id, (r.deleted_at IS NOT NULL)::BOOLEAN AS removed,
       (latest.id IS NOT NULL)::BOOLEAN AS has_unrestored_removal,
       EXISTS (
           SELECT 1 FROM user_reports ur
           WHERE ur.review_id = r.id AND ur.status = 'closed' AND ur.outcome = 'review_removal'
             AND ur.closed_at = latest.created_at
       )::BOOLEAN AS removed_by_report,
       EXISTS (
           SELECT 1 FROM user_reports ur
           WHERE ur.review_id = r.id AND ur.status = 'closed' AND ur.outcome = 'no_action'
             AND ur.reporter_id = @reporter_id::UUID
       )::BOOLEAN AS retained_by_report
FROM reviews r
LEFT JOIN LATERAL (
    SELECT rr.id, rr.created_at
    FROM review_removals rr
    WHERE rr.review_id = r.id
      AND NOT EXISTS (SELECT 1 FROM review_restorations x WHERE x.removal_id = rr.id)
    ORDER BY rr.created_at DESC, rr.id DESC
    LIMIT 1
) latest ON true
WHERE r.id = @id::UUID;

-- Reads

-- name: GetAppeal :one
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at,
       appellant.display_name AS appellant_name,
       closer.display_name AS closer_name
FROM user_appeals a
JOIN users appellant ON appellant.id = a.appellant_id
LEFT JOIN users closer ON closer.id = a.closed_by
WHERE a.id = @id::UUID;

-- Appeals the user filed or is a counterpart of.
-- name: ListAppealsInvolving :many
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at,
       appellant.display_name AS appellant_name,
       closer.display_name AS closer_name
FROM user_appeals a
JOIN users appellant ON appellant.id = a.appellant_id
LEFT JOIN users closer ON closer.id = a.closed_by
WHERE a.appellant_id = @user_id::UUID
   OR EXISTS (
       SELECT 1 FROM user_appeal_decisions d WHERE d.appeal_id = a.id AND d.counterpart_id = @user_id::UUID
   )
ORDER BY a.created_at DESC, a.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountAppealsInvolving :one
SELECT COUNT(*)
FROM user_appeals a
WHERE a.appellant_id = @user_id::UUID
   OR EXISTS (
       SELECT 1 FROM user_appeal_decisions d WHERE d.appeal_id = a.id AND d.counterpart_id = @user_id::UUID
   );

-- name: ListAppeals :many
SELECT a.id, a.appellant_id, a.request_id, a.reason, a.status, a.awaited_user_id,
       a.closed_at, a.closed_by, a.created_at, a.updated_at,
       appellant.display_name AS appellant_name,
       closer.display_name AS closer_name
FROM user_appeals a
JOIN users appellant ON appellant.id = a.appellant_id
LEFT JOIN users closer ON closer.id = a.closed_by
WHERE (sqlc.narg('status')::TEXT IS NULL OR a.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('appellant_id')::UUID IS NULL OR a.appellant_id = sqlc.narg('appellant_id')::UUID)
ORDER BY a.created_at DESC, a.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountAppeals :one
SELECT COUNT(*)
FROM user_appeals a
WHERE (sqlc.narg('status')::TEXT IS NULL OR a.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('appellant_id')::UUID IS NULL OR a.appellant_id = sqlc.narg('appellant_id')::UUID);

-- name: ListDecisions :many
SELECT d.id, d.appeal_id, d.kind, d.report_id, d.warning_id, d.review_id, d.counterpart_id,
       d.outcome, d.outcome_reason,
       counterpart.display_name AS counterpart_name
FROM user_appeal_decisions d
LEFT JOIN users counterpart ON counterpart.id = d.counterpart_id
WHERE d.appeal_id = ANY(@appeal_ids::UUID[])
ORDER BY d.appeal_id, d.created_at, d.id;

-- name: ListAppealResponses :many
SELECT ar.id, ar.appeal_id, ar.responder_id, ar.body, ar.created_at,
       responder.display_name AS responder_name
FROM user_appeal_responses ar
JOIN users responder ON responder.id = ar.responder_id
WHERE ar.appeal_id = @appeal_id::UUID
ORDER BY ar.created_at, ar.id;

-- Lifecycle

-- name: LockAppeal :one
SELECT appellant_id, status, awaited_user_id
FROM user_appeals
WHERE id = @id::UUID
FOR UPDATE;

-- Decisions do not change while the appeal row is locked, except through
-- ResolveDecision under that lock. The report columns describe what an
-- overturned report outcome must undo.
-- name: LockedDecisions :many
SELECT d.id, d.kind, d.report_id, d.warning_id, d.review_id, d.counterpart_id,
       r.outcome AS report_outcome, r.subject_id AS report_subject_id, r.review_id AS report_review_id,
       -- The nil UUID when the report outcome recorded no warning.
       COALESCE(w.id, '00000000-0000-0000-0000-000000000000')::UUID AS report_warning_id
FROM user_appeal_decisions d
LEFT JOIN user_reports r ON r.id = d.report_id
LEFT JOIN LATERAL (
    SELECT aw.id FROM account_warnings aw
    WHERE aw.report_id = r.id AND aw.deleted_at IS NULL
    ORDER BY aw.created_at DESC, aw.id DESC
    LIMIT 1
) w ON true
WHERE d.appeal_id = @appeal_id::UUID
ORDER BY d.created_at, d.id;

-- name: TransitionAppeal :execrows
UPDATE user_appeals
SET status = @to_status::TEXT,
    awaited_user_id = sqlc.narg(awaited_user_id)::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status = @from_status::TEXT;

-- name: CloseAppeal :execrows
UPDATE user_appeals
SET status = 'closed',
    awaited_user_id = NULL,
    closed_at = now(),
    closed_by = @closed_by::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status <> 'closed';

-- name: ResolveDecision :execrows
UPDATE user_appeal_decisions
SET outcome = @outcome::TEXT,
    outcome_reason = @outcome_reason::TEXT,
    active = false
WHERE id = @id::UUID AND appeal_id = @appeal_id::UUID AND active;

-- name: InsertAppealResponse :exec
INSERT INTO user_appeal_responses (id, appeal_id, responder_id, body)
VALUES (@id::UUID, @appeal_id::UUID, @responder_id::UUID, @body::TEXT);
