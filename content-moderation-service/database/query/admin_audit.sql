-- name: InsertAdminAudit :exec
INSERT INTO moderation_admin_audit (action, job_id, config_version_id, actor, detail)
VALUES (
    sqlc.arg(action),
    sqlc.narg(job_id),
    sqlc.narg(config_version_id),
    sqlc.arg(actor),
    sqlc.narg(detail)
);
