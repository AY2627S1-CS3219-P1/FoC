-- Reports and the reported participant's responses. A report targets exactly
-- one review or one order. User, review and order IDs are owned by User and
-- Order Service, so they carry no foreign keys here.
--
-- Lifecycle: open -> under_review <-> awaiting_response -> closed. Closing
-- records an outcome and its reason; User Service applies warning, suspension
-- and review-removal outcomes from the outcome outbox.
-- +goose Up
CREATE TABLE reports (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id    UUID NOT NULL,
    subject_id     UUID NOT NULL,
    review_id      UUID,
    order_id       UUID,
    -- The originating order: the order target, or the reported review's order.
    request_id     UUID NOT NULL,
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
    closed_by      UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT reports_not_self CHECK (reporter_id <> subject_id),
    CONSTRAINT reports_one_target CHECK ((review_id IS NULL) <> (order_id IS NULL)),
    -- A closed report, and only a closed report, has an outcome, reason and time.
    CONSTRAINT reports_closed_outcome CHECK (
        (status = 'closed') = (closed_at IS NOT NULL)
        AND (status = 'closed') = (outcome IS NOT NULL)
        AND (status = 'closed') = (outcome_reason IS NOT NULL)
    ),
    CONSTRAINT reports_review_removal_target CHECK (outcome <> 'review_removal' OR review_id IS NOT NULL)
);
CREATE INDEX idx_reports_status ON reports (status, created_at DESC);
CREATE INDEX idx_reports_reporter ON reports (reporter_id, created_at DESC);
CREATE INDEX idx_reports_subject ON reports (subject_id, created_at DESC);
CREATE INDEX idx_reports_review ON reports (review_id) WHERE review_id IS NOT NULL;
CREATE INDEX idx_reports_order ON reports (order_id) WHERE order_id IS NOT NULL;
-- A reporter keeps at most one unclosed report per target.
CREATE UNIQUE INDEX idx_reports_active_review
    ON reports (reporter_id, review_id)
    WHERE status <> 'closed' AND review_id IS NOT NULL;
CREATE UNIQUE INDEX idx_reports_active_order
    ON reports (reporter_id, order_id, subject_id)
    WHERE status <> 'closed' AND order_id IS NOT NULL;

-- The reported participant's replies. Responses are append-only.
CREATE TABLE report_responses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id    UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    responder_id UUID NOT NULL,
    body         TEXT NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_report_responses_report ON report_responses (report_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS report_responses;
DROP TABLE IF EXISTS reports;
