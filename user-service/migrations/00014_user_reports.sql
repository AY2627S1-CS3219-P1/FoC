-- User reports and the reported participant's responses. A report targets
-- exactly one review or one order. For a review, the subject is the review's
-- author; for an order, the reporter names the other participant. order_id has
-- no foreign key because the order service owns orders.
--
-- Lifecycle: open -> under_review <-> awaiting_response -> closed. Closing
-- records an outcome and its reason; warning, suspension and review-removal
-- outcomes are applied in the same transaction and reference the report.
-- +goose Up
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

-- +goose Down
ALTER TABLE account_warnings DROP CONSTRAINT IF EXISTS account_warnings_report_id_fkey;
ALTER TABLE role_changes DROP CONSTRAINT IF EXISTS role_changes_report_id_fkey;
DROP TABLE IF EXISTS user_report_responses;
DROP TABLE IF EXISTS user_reports;
