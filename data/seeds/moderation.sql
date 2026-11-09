-- Seed a usable review moderation configuration for local development.
-- This only initializes an empty configuration state; reruns and existing
-- admin configuration must remain untouched.
BEGIN;

INSERT INTO moderation_config_state (id)
VALUES (true)
ON CONFLICT (id) DO NOTHING;

WITH inserted AS (
    INSERT INTO moderation_config_versions (
        model_id,
        parameters,
        output_mode,
        policy_version,
        provider_fingerprint,
        created_by
    )
    SELECT
        'gemma4:26b',
        '{"temperature":0,"max_tokens":512}'::jsonb,
        'json_schema',
        'review-text-v1',
        '1a528ad36ea562eecd33e6b31bea8095e69b2019364b27cd7eaa1ad27e248735',
        'demo-seed'
    WHERE NOT EXISTS (
        SELECT 1
        FROM moderation_config_state
        WHERE id = true AND active_version_id IS NOT NULL
    )
      AND NOT EXISTS (
        SELECT 1
        FROM moderation_config_versions
        WHERE created_by = 'demo-seed'
    )
    RETURNING version_id
), activated AS (
    UPDATE moderation_config_state
    SET active_version_id = inserted.version_id,
        activated_by = 'demo-seed',
        activated_at = now(),
        updated_at = now()
    FROM inserted
    WHERE moderation_config_state.id = true
      AND moderation_config_state.active_version_id IS NULL
    RETURNING inserted.version_id
)
INSERT INTO moderation_admin_audit (action, config_version_id, actor, detail)
SELECT 'ACTIVATE', version_id, 'demo-seed', '{"model_id":"gemma4:26b","policy_version":"review-text-v1"}'::jsonb
FROM activated;

COMMIT;
