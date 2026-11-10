-- Display names are not stored here: the service fills them from User
-- Service at read time.

-- name: InsertReport :exec
INSERT INTO reports (id, reporter_id, subject_id, review_id, order_id, request_id, category, description)
VALUES (
    @id::UUID,
    @reporter_id::UUID,
    @subject_id::UUID,
    sqlc.narg('review_id')::UUID,
    sqlc.narg('order_id')::UUID,
    @request_id::UUID,
    @category,
    @description
);

-- name: GetReport :one
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at
FROM reports r
WHERE r.id = @id::UUID;

-- Reports the user filed or is the subject of.
-- name: ListReportsInvolving :many
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at
FROM reports r
WHERE r.reporter_id = @user_id::UUID OR r.subject_id = @user_id::UUID
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::BIGINT OFFSET @page_offset::BIGINT;

-- name: CountReportsInvolving :one
SELECT COUNT(*)
FROM reports
WHERE reporter_id = @user_id::UUID OR subject_id = @user_id::UUID;

-- Each filter is optional; a NULL filter matches every row.
-- name: ListReports :many
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at
FROM reports r
WHERE (sqlc.narg('status')::TEXT IS NULL OR r.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('subject_id')::UUID IS NULL OR r.subject_id = sqlc.narg('subject_id')::UUID)
  AND (sqlc.narg('review_id')::UUID IS NULL OR r.review_id = sqlc.narg('review_id')::UUID)
  AND (sqlc.narg('order_id')::UUID IS NULL OR r.order_id = sqlc.narg('order_id')::UUID)
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::BIGINT OFFSET @page_offset::BIGINT;

-- name: CountReports :one
SELECT COUNT(*)
FROM reports r
WHERE (sqlc.narg('status')::TEXT IS NULL OR r.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('subject_id')::UUID IS NULL OR r.subject_id = sqlc.narg('subject_id')::UUID)
  AND (sqlc.narg('review_id')::UUID IS NULL OR r.review_id = sqlc.narg('review_id')::UUID)
  AND (sqlc.narg('order_id')::UUID IS NULL OR r.order_id = sqlc.narg('order_id')::UUID);

-- Locks the report so concurrent transitions serialize. request_id is the
-- originating order: the order target, or the reported review's order,
-- recorded when the report was filed.
-- name: LockReport :one
SELECT reporter_id, subject_id, review_id, status, request_id
FROM reports
WHERE id = @id::UUID
FOR UPDATE;

-- name: TransitionReport :execrows
UPDATE reports
SET status = @to_status::TEXT, updated_at = now()
WHERE id = @id::UUID AND status = @from_status::TEXT;

-- name: CloseReport :execrows
UPDATE reports
SET status = 'closed',
    outcome = @outcome::TEXT,
    outcome_reason = @outcome_reason::TEXT,
    closed_at = now(),
    closed_by = @closed_by::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status <> 'closed';

-- name: InsertResponse :exec
INSERT INTO report_responses (id, report_id, responder_id, body)
VALUES (@id::UUID, @report_id::UUID, @responder_id::UUID, @body);

-- name: ListResponses :many
SELECT rr.id, rr.report_id, rr.responder_id, rr.body, rr.created_at
FROM report_responses rr
WHERE rr.report_id = @report_id::UUID
ORDER BY rr.created_at, rr.id;
