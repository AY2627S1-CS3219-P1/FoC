-- Reports, appeals and their evidence moved to moderation-service, which owns
-- them in its own database. User Service keeps only the consequences it
-- applies: role changes, warnings, review removals and restorations still
-- record the report and appeal they answer, as opaque IDs without foreign
-- keys, like order_id. Existing local rows are not copied; the stack that
-- created these tables never reached main.
-- +goose Up
ALTER TABLE role_changes DROP CONSTRAINT IF EXISTS role_changes_report_id_fkey;
ALTER TABLE account_warnings DROP CONSTRAINT IF EXISTS account_warnings_report_id_fkey;
ALTER TABLE account_warnings DROP CONSTRAINT IF EXISTS account_warnings_appeal_id_fkey;
ALTER TABLE review_restorations DROP CONSTRAINT IF EXISTS review_restorations_appeal_id_fkey;

DROP TABLE IF EXISTS evidence_attachments;
DROP TABLE IF EXISTS user_appeal_responses;
DROP TABLE IF EXISTS user_appeal_decisions;
DROP TABLE IF EXISTS user_appeals;
DROP TABLE IF EXISTS user_report_responses;
DROP TABLE IF EXISTS user_reports;

-- The report whose outcome removed the review, so an appeal of that removal
-- goes through the report, and the author's own removal list leaves it out.
ALTER TABLE review_removals ADD COLUMN report_id UUID;
CREATE INDEX idx_review_removals_report ON review_removals (report_id) WHERE report_id IS NOT NULL;

-- Moderation outcome events already applied, so a redelivered event is a no-op.
CREATE TABLE moderation_outcome_events (
    event_id   UUID PRIMARY KEY,
    outcome    TEXT NOT NULL CHECK (outcome IN ('applied', 'rejected')),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS moderation_outcome_events;

DROP INDEX IF EXISTS idx_review_removals_report;
ALTER TABLE review_removals DROP COLUMN IF EXISTS report_id;

-- Recreate the moderation tables as 00014, 00015 and 00017 left them, empty.

CREATE TABLE user_reports (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    subject_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    review_id      UUID REFERENCES reviews (id),
    order_id       UUID,
    category       TEXT NOT NULL CHECK (
        category IN ('harassment', 'fraud', 'no_show', 'inappropriate_content', 'other')
    ),
    description    TEXT NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 4000),
    status         TEXT NOT NULL DEFAULT 'open' CHECK (
        status IN ('open', 'under_review', 'awaiting_response', 'closed')
    ),
    outcome        TEXT CHECK (outcome IN ('no_action', 'warning', 'suspension', 'review_removal')),
    outcome_reason TEXT CHECK (length(btrim(outcome_reason)) BETWEEN 1 AND 2000),
    closed_at      TIMESTAMPTZ,
    closed_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_reports_not_self CHECK (reporter_id <> subject_id),
    CONSTRAINT user_reports_one_target CHECK ((review_id IS NULL) <> (order_id IS NULL)),
    -- A closed report, and only a closed report, has an outcome, reason and time.
    CONSTRAINT user_reports_closed_outcome CHECK (
        (status = 'closed') = (closed_at IS NOT NULL)
        AND (status = 'closed') = (outcome IS NOT NULL)
        AND (status = 'closed') = (outcome_reason IS NOT NULL)
    ),
    CONSTRAINT user_reports_review_removal_target CHECK (outcome <> 'review_removal' OR review_id IS NOT NULL)
);
CREATE INDEX idx_user_reports_status ON user_reports (status, created_at DESC);
CREATE INDEX idx_user_reports_reporter ON user_reports (reporter_id, created_at DESC);
CREATE INDEX idx_user_reports_subject ON user_reports (subject_id, created_at DESC);
CREATE INDEX idx_user_reports_review ON user_reports (review_id) WHERE review_id IS NOT NULL;
CREATE INDEX idx_user_reports_order ON user_reports (order_id) WHERE order_id IS NOT NULL;
-- A reporter keeps at most one unclosed report per target.
CREATE UNIQUE INDEX idx_user_reports_active_review
    ON user_reports (reporter_id, review_id)
    WHERE status <> 'closed' AND review_id IS NOT NULL;
CREATE UNIQUE INDEX idx_user_reports_active_order
    ON user_reports (reporter_id, order_id, subject_id)
    WHERE status <> 'closed' AND order_id IS NOT NULL;

-- The reported participant's replies. Responses are append-only.
CREATE TABLE user_report_responses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id    UUID NOT NULL REFERENCES user_reports (id) ON DELETE CASCADE,
    responder_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body         TEXT NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_report_responses_report ON user_report_responses (report_id, created_at);

ALTER TABLE role_changes
    ADD CONSTRAINT role_changes_report_id_fkey
    FOREIGN KEY (report_id) REFERENCES user_reports (id) ON DELETE SET NULL;
ALTER TABLE account_warnings
    ADD CONSTRAINT account_warnings_report_id_fkey
    FOREIGN KEY (report_id) REFERENCES user_reports (id) ON DELETE SET NULL;

CREATE TABLE user_appeals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appellant_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    request_id      UUID,
    reason          TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 4000),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (
        status IN ('open', 'under_review', 'awaiting_response', 'closed')
    ),
    -- The participant an administrator asked to respond. Cleared by the reply.
    awaited_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    -- The client's key for this submission; a retry returns the same appeal.
    idempotency_key UUID NOT NULL,
    -- SHA-256 of the canonical submission, so a reused key with a different
    -- payload is rejected rather than silently answered with another appeal.
    request_hash    TEXT NOT NULL CHECK (length(request_hash) = 64),
    closed_at       TIMESTAMPTZ,
    closed_by       UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_appeals_idempotency UNIQUE (appellant_id, idempotency_key),
    CONSTRAINT user_appeals_closed CHECK ((status = 'closed') = (closed_at IS NOT NULL)),
    CONSTRAINT user_appeals_awaited CHECK (awaited_user_id IS NULL OR status = 'awaiting_response')
);
CREATE INDEX idx_user_appeals_status ON user_appeals (status, created_at DESC);
CREATE INDEX idx_user_appeals_appellant ON user_appeals (appellant_id, created_at DESC);

-- One row per challenged decision. Exactly the target column for the kind is
-- set: report_outcome uses report_id, warning uses warning_id, and both review
-- kinds use review_id. credit_adjustment is reserved until Credit records
-- administrative adjustments (#302).
CREATE TABLE user_appeal_decisions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appeal_id       UUID NOT NULL REFERENCES user_appeals (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (
        kind IN ('report_outcome', 'warning', 'review_removal', 'review_retention', 'credit_adjustment')
    ),
    report_id       UUID REFERENCES user_reports (id) ON DELETE CASCADE,
    warning_id      UUID REFERENCES account_warnings (id) ON DELETE CASCADE,
    review_id       UUID REFERENCES reviews (id) ON DELETE CASCADE,
    credit_entry_id UUID,
    -- The other participant affected by the decision, who may be asked to
    -- respond and may read the appeal. NULL when nobody else is affected.
    counterpart_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    -- Cleared when the appeal closes; only active decisions are unique.
    active          BOOLEAN NOT NULL DEFAULT true,
    outcome         TEXT CHECK (outcome IN ('upheld', 'overturned')),
    outcome_reason  TEXT CHECK (length(btrim(outcome_reason)) BETWEEN 1 AND 2000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_appeal_decisions_target CHECK (
        CASE kind
            WHEN 'report_outcome' THEN num_nonnulls(report_id, warning_id, review_id, credit_entry_id) = 1
                AND report_id IS NOT NULL
            WHEN 'warning' THEN num_nonnulls(report_id, warning_id, review_id, credit_entry_id) = 1
                AND warning_id IS NOT NULL
            WHEN 'credit_adjustment' THEN num_nonnulls(report_id, warning_id, review_id, credit_entry_id) = 1
                AND credit_entry_id IS NOT NULL
            ELSE num_nonnulls(report_id, warning_id, review_id, credit_entry_id) = 1
                AND review_id IS NOT NULL
        END
    ),
    CONSTRAINT user_appeal_decisions_credit_unsupported CHECK (kind <> 'credit_adjustment'),
    -- A decision has an outcome and its reason exactly when its appeal closed.
    CONSTRAINT user_appeal_decisions_outcome CHECK (
        (outcome IS NULL) = (outcome_reason IS NULL) AND active = (outcome IS NULL)
    )
);
CREATE INDEX idx_user_appeal_decisions_appeal ON user_appeal_decisions (appeal_id, created_at);
CREATE INDEX idx_user_appeal_decisions_counterpart
    ON user_appeal_decisions (counterpart_id) WHERE counterpart_id IS NOT NULL;
-- A decision is challenged by at most one unclosed appeal.
CREATE UNIQUE INDEX idx_user_appeal_decisions_active
    ON user_appeal_decisions (kind, COALESCE(report_id, warning_id, review_id, credit_entry_id))
    WHERE active;

-- Replies from the participant an administrator asked to respond. Append-only.
CREATE TABLE user_appeal_responses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appeal_id    UUID NOT NULL REFERENCES user_appeals (id) ON DELETE CASCADE,
    responder_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body         TEXT NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_appeal_responses_appeal ON user_appeal_responses (appeal_id, created_at);

ALTER TABLE account_warnings
    ADD CONSTRAINT account_warnings_appeal_id_fkey
    FOREIGN KEY (appeal_id) REFERENCES user_appeals (id) ON DELETE SET NULL;

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

ALTER TABLE review_restorations
    ADD CONSTRAINT review_restorations_appeal_id_fkey
    FOREIGN KEY (appeal_id) REFERENCES user_appeals (id) ON DELETE SET NULL;
