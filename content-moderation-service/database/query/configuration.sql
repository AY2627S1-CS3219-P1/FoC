-- name: EnsureConfigStateSingleton :exec
INSERT INTO moderation_config_state (id)
VALUES (true)
ON CONFLICT DO NOTHING;

-- name: GetActiveConfigVersion :one
SELECT active_version_id FROM moderation_config_state;

-- name: InsertConfigVersion :one
INSERT INTO moderation_config_versions (
    model_id, parameters, output_mode, policy_version, provider_fingerprint, created_by
) VALUES (
    sqlc.arg(model_id), sqlc.arg(parameters), sqlc.arg(output_mode),
    sqlc.arg(policy_version), sqlc.arg(provider_fingerprint), sqlc.arg(created_by)
)
RETURNING version_id;

-- name: GetActiveConfigVersionFull :one
SELECT v.* FROM moderation_config_versions v
JOIN moderation_config_state s ON s.active_version_id = v.version_id;

-- name: ActivateConfigVersion :execrows
-- NULL expects no active version; all replacements must name the active version.
UPDATE moderation_config_state
SET active_version_id = sqlc.arg(active_version_id),
    activated_by = sqlc.arg(activated_by),
    activated_at = now(),
    updated_at = now()
WHERE active_version_id IS NOT DISTINCT FROM sqlc.narg(expected_version_id)::uuid;

-- name: GetConfigState :one
-- The singleton moderation config state: the active version pointer and the
-- activation metadata. A zero active_version_id means no configuration has been
-- activated. Callers must first ensure the singleton row exists (see
-- EnsureConfigStateSingleton) so this returns a row rather than "no rows".
SELECT active_version_id, activated_by, activated_at FROM moderation_config_state;

-- name: GetConfigVersionById :one
-- An immutable configuration version, by id. Used to retrieve a persisted draft
-- before it is activated.
SELECT * FROM moderation_config_versions
WHERE version_id = sqlc.arg(version_id);

-- name: PauseConfiguration :exec
INSERT INTO moderation_config_pauses (version_id) VALUES (sqlc.arg(version_id))
ON CONFLICT DO NOTHING;

-- name: UnpauseConfiguration :exec
DELETE FROM moderation_config_pauses WHERE version_id = sqlc.arg(version_id);
