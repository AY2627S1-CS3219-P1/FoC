-- name: InsertAttempt :exec
INSERT INTO moderation_attempts (
    job_id, run_generation, attempt_number, model_id
) VALUES (
    sqlc.arg(job_id), sqlc.arg(run_generation), sqlc.arg(attempt_number),
    sqlc.narg(model_id)
);

-- name: FinishAttempt :exec
-- Success: outcome='completed', no error class. Failure: empty outcome, error
-- class plus a bounded, sanitized diagnostic. Unconditional so one shape serves
-- both; only started-but-unfinished attempts are finished.
UPDATE moderation_attempts
SET finished_at = now(),
    outcome = sqlc.arg(outcome),
    error_class = sqlc.arg(error_class),
    detail = sqlc.narg(detail)
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
  AND attempt_number = sqlc.arg(attempt_number)
  AND finished_at IS NULL;

-- name: CountAttemptsByClass :one
SELECT count(*)::int
FROM moderation_attempts
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
  AND error_class = sqlc.arg(error_class);

-- name: GetAttempts :many
SELECT * FROM moderation_attempts
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
ORDER BY attempt_number ASC;

