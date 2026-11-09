-- +goose Up
ALTER TABLE moderation_admin_audit DROP CONSTRAINT moderation_admin_audit_action_check;
ALTER TABLE moderation_admin_audit ADD CONSTRAINT moderation_admin_audit_action_check
    CHECK (action IN ('ACTIVATE_CONFIG', 'RETRY_JOB', 'RESUME_CONFIGURATION_PAUSED_JOB', 'SAVE_DRAFT'));

-- +goose Down
-- Preserve draft history while restoring the previous vocabulary for new writes.
ALTER TABLE moderation_admin_audit DROP CONSTRAINT moderation_admin_audit_action_check;
ALTER TABLE moderation_admin_audit ADD CONSTRAINT moderation_admin_audit_action_check
    CHECK (action IN ('ACTIVATE_CONFIG', 'RETRY_JOB', 'RESUME_CONFIGURATION_PAUSED_JOB')) NOT VALID;
