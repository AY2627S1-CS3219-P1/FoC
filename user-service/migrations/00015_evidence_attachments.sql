-- Image evidence for reports, report responses, appeals and appeal responses
-- (RP1.6, RP2.2, RP8.2, RP9.4). Uploads follow review photos: the browser puts
-- the image straight into object storage, the row is staged as 'uploading',
-- and the submission transaction attaches it to exactly one parent. Unattached
-- rows are reaped by the cleanup loop.
-- +goose Up
CREATE TABLE evidence_attachments (
    id                 UUID        PRIMARY KEY,
    owner_id           UUID        REFERENCES users (id) ON DELETE SET NULL,
    object_key         TEXT        NOT NULL UNIQUE,
    content_type       TEXT        NOT NULL CHECK (content_type IN (
                           'image/jpeg', 'image/png', 'image/webp',
                           'image/heic', 'image/heif'
                       )),
    size_bytes         BIGINT      NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
    state              TEXT        NOT NULL DEFAULT 'uploading'
                       CHECK (state IN ('uploading', 'attached', 'deleting')),
    report_id          UUID        REFERENCES user_reports (id) ON DELETE CASCADE,
    report_response_id UUID        REFERENCES user_report_responses (id) ON DELETE CASCADE,
    appeal_id          UUID        REFERENCES user_appeals (id) ON DELETE CASCADE,
    appeal_response_id UUID        REFERENCES user_appeal_responses (id) ON DELETE CASCADE,
    ordinal            INTEGER     CHECK (ordinal BETWEEN 0 AND 4),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    upload_expires_at  TIMESTAMPTZ NOT NULL,
    -- An attached image has exactly one parent and a position; a staged one
    -- has neither.
    CONSTRAINT evidence_attachments_parent CHECK (
        (state = 'attached') = (num_nonnulls(report_id, report_response_id, appeal_id, appeal_response_id) = 1)
        AND num_nonnulls(report_id, report_response_id, appeal_id, appeal_response_id) <= 1
        AND (ordinal IS NOT NULL) = (state = 'attached')
    ),
    UNIQUE (report_id, ordinal),
    UNIQUE (report_response_id, ordinal),
    UNIQUE (appeal_id, ordinal),
    UNIQUE (appeal_response_id, ordinal)
);

CREATE INDEX evidence_attachments_owner ON evidence_attachments (owner_id);
CREATE INDEX evidence_attachments_cleanup ON evidence_attachments (created_at) WHERE state <> 'attached';

-- +goose Down
DROP TABLE evidence_attachments;
