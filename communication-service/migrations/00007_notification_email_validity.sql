-- +goose Up
-- Action emails keep their delivery class while waiting for an address or SMTP.
ALTER TABLE notification_emails
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN order_id uuid,
    ADD COLUMN order_revision bigint,
    ADD COLUMN unless_reassigned boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT notification_email_order_validity CHECK ((order_id IS NULL) = (order_revision IS NULL));
ALTER TABLE notification_emails DROP CONSTRAINT notification_emails_status_check;
ALTER TABLE notification_emails ADD CONSTRAINT notification_emails_status_check
    CHECK (status IN ('pending', 'rejected', 'skipped'));

-- +goose Down
-- Older workers treat these terminal rows as accepted rejections as well.
UPDATE notification_emails SET status = 'rejected' WHERE status = 'skipped';
ALTER TABLE notification_emails DROP CONSTRAINT notification_emails_status_check;
ALTER TABLE notification_emails ADD CONSTRAINT notification_emails_status_check CHECK (status IN ('pending', 'rejected'));
ALTER TABLE notification_emails
    DROP CONSTRAINT notification_email_order_validity,
    DROP COLUMN unless_reassigned,
    DROP COLUMN order_revision,
    DROP COLUMN order_id,
    DROP COLUMN expires_at;
