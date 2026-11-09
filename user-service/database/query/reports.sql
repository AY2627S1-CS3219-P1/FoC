-- name: UserExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE id = @id::UUID) AS user_exists;

-- Returns the author of a live review. A removed review cannot be newly reported.
-- name: LiveReviewAuthor :one
SELECT reviewer_id
FROM reviews
WHERE id = @id::UUID AND deleted_at IS NULL;

-- name: InsertReport :exec
INSERT INTO user_reports (id, reporter_id, subject_id, review_id, order_id, category, description)
VALUES (
    @id::UUID,
    @reporter_id::UUID,
    @subject_id::UUID,
    sqlc.narg('review_id')::UUID,
    sqlc.narg('order_id')::UUID,
    @category,
    @description
);

-- name: GetReport :one
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at,
       reporter.display_name AS reporter_name,
       subject.display_name AS subject_name,
       closer.display_name AS closer_name
FROM user_reports r
JOIN users reporter ON reporter.id = r.reporter_id
JOIN users subject ON subject.id = r.subject_id
LEFT JOIN users closer ON closer.id = r.closed_by
WHERE r.id = @id::UUID;

-- Reports the user filed or is the subject of.
-- name: ListReportsInvolving :many
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at,
       reporter.display_name AS reporter_name,
       subject.display_name AS subject_name,
       closer.display_name AS closer_name
FROM user_reports r
JOIN users reporter ON reporter.id = r.reporter_id
JOIN users subject ON subject.id = r.subject_id
LEFT JOIN users closer ON closer.id = r.closed_by
WHERE r.reporter_id = @user_id::UUID OR r.subject_id = @user_id::UUID
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::BIGINT OFFSET @page_offset::BIGINT;

-- name: CountReportsInvolving :one
SELECT COUNT(*)
FROM user_reports
WHERE reporter_id = @user_id::UUID OR subject_id = @user_id::UUID;

-- Each filter is optional; a NULL filter matches every row.
-- name: ListReports :many
SELECT r.id, r.reporter_id, r.subject_id, r.review_id, r.order_id, r.category, r.description, r.status,
       r.outcome, r.outcome_reason, r.closed_at, r.closed_by, r.created_at, r.updated_at,
       reporter.display_name AS reporter_name,
       subject.display_name AS subject_name,
       closer.display_name AS closer_name
FROM user_reports r
JOIN users reporter ON reporter.id = r.reporter_id
JOIN users subject ON subject.id = r.subject_id
LEFT JOIN users closer ON closer.id = r.closed_by
WHERE (sqlc.narg('status')::TEXT IS NULL OR r.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('subject_id')::UUID IS NULL OR r.subject_id = sqlc.narg('subject_id')::UUID)
  AND (sqlc.narg('review_id')::UUID IS NULL OR r.review_id = sqlc.narg('review_id')::UUID)
  AND (sqlc.narg('order_id')::UUID IS NULL OR r.order_id = sqlc.narg('order_id')::UUID)
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::BIGINT OFFSET @page_offset::BIGINT;

-- name: CountReports :one
SELECT COUNT(*)
FROM user_reports r
WHERE (sqlc.narg('status')::TEXT IS NULL OR r.status = sqlc.narg('status')::TEXT)
  AND (sqlc.narg('subject_id')::UUID IS NULL OR r.subject_id = sqlc.narg('subject_id')::UUID)
  AND (sqlc.narg('review_id')::UUID IS NULL OR r.review_id = sqlc.narg('review_id')::UUID)
  AND (sqlc.narg('order_id')::UUID IS NULL OR r.order_id = sqlc.narg('order_id')::UUID);

-- Locks the report so concurrent transitions serialize. request_id is the
-- originating order: the order target, or the reported review's order. Only the
-- report row is locked; a review's order_id never changes.
-- name: LockReport :one
SELECT r.reporter_id, r.subject_id, r.review_id, r.status,
       COALESCE(r.order_id, rv.order_id)::UUID AS request_id
FROM user_reports r
LEFT JOIN reviews rv ON rv.id = r.review_id
WHERE r.id = @id::UUID
FOR UPDATE OF r;

-- name: TransitionReport :execrows
UPDATE user_reports
SET status = @to_status::TEXT, updated_at = now()
WHERE id = @id::UUID AND status = @from_status::TEXT;

-- name: CloseReport :execrows
UPDATE user_reports
SET status = 'closed',
    outcome = @outcome::TEXT,
    outcome_reason = @outcome_reason::TEXT,
    closed_at = now(),
    closed_by = @closed_by::UUID,
    updated_at = now()
WHERE id = @id::UUID AND status <> 'closed';

-- name: InsertResponse :exec
INSERT INTO user_report_responses (id, report_id, responder_id, body)
VALUES (@id::UUID, @report_id::UUID, @responder_id::UUID, @body);

-- name: ListResponses :many
SELECT rr.id, rr.report_id, rr.responder_id, rr.body, rr.created_at,
       responder.display_name AS responder_name
FROM user_report_responses rr
JOIN users responder ON responder.id = rr.responder_id
WHERE rr.report_id = @report_id::UUID
ORDER BY rr.created_at, rr.id;
