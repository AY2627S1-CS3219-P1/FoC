-- Appeals (RP7-RP12). The affected user appeals one or more final decisions:
-- a closed report's outcome, an account warning, a review removal, or a
-- decision to keep a reported review. Warning and review IDs are owned by User
-- Service, so they carry no foreign keys here.
--
-- Lifecycle: open -> under_review <-> awaiting_response -> closed, as for
-- reports. Overturned outcomes are applied by User Service from the outbox.
-- +goose Up
CREATE TABLE appeals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appellant_id    UUID NOT NULL,
    request_id      UUID,
    reason          TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 4000),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (
        status IN ('open', 'under_review', 'awaiting_response', 'closed')
    ),
    -- The participant an administrator asked to respond. Cleared by the reply.
    awaited_user_id UUID,
    -- The client's key for this submission; a retry returns the same appeal.
    idempotency_key UUID NOT NULL,
    -- SHA-256 of the canonical submission, so a reused key with a different
    -- payload is rejected rather than silently answered with another appeal.
    request_hash    TEXT NOT NULL CHECK (length(request_hash) = 64),
    closed_at       TIMESTAMPTZ,
    closed_by       UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT appeals_idempotency UNIQUE (appellant_id, idempotency_key),
    CONSTRAINT appeals_closed CHECK ((status = 'closed') = (closed_at IS NOT NULL)),
    CONSTRAINT appeals_awaited CHECK (awaited_user_id IS NULL OR status = 'awaiting_response')
);
CREATE INDEX idx_appeals_status ON appeals (status, created_at DESC);
CREATE INDEX idx_appeals_appellant ON appeals (appellant_id, created_at DESC);

-- One row per challenged decision. Exactly the target column for the kind is
-- set: report_outcome uses report_id, warning uses warning_id, and both review
-- kinds use review_id. credit_adjustment is reserved until Credit records
-- administrative adjustments (#302).
CREATE TABLE appeal_decisions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appeal_id       UUID NOT NULL REFERENCES appeals (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (
        kind IN ('report_outcome', 'warning', 'review_removal', 'review_retention', 'credit_adjustment')
    ),
    report_id       UUID REFERENCES reports (id) ON DELETE CASCADE,
    warning_id      UUID,
    review_id       UUID,
    credit_entry_id UUID,
    -- The other participant affected by the decision, who may be asked to
    -- respond and may read the appeal. NULL when nobody else is affected.
    counterpart_id  UUID,
    -- Cleared when the appeal closes; only active decisions are unique.
    active          BOOLEAN NOT NULL DEFAULT true,
    outcome         TEXT CHECK (outcome IN ('upheld', 'overturned')),
    outcome_reason  TEXT CHECK (length(btrim(outcome_reason)) BETWEEN 1 AND 2000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT appeal_decisions_target CHECK (
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
    CONSTRAINT appeal_decisions_credit_unsupported CHECK (kind <> 'credit_adjustment'),
    -- A decision has an outcome and its reason exactly when its appeal closed.
    CONSTRAINT appeal_decisions_outcome CHECK (
        (outcome IS NULL) = (outcome_reason IS NULL) AND active = (outcome IS NULL)
    )
);
CREATE INDEX idx_appeal_decisions_appeal ON appeal_decisions (appeal_id, created_at);
CREATE INDEX idx_appeal_decisions_counterpart
    ON appeal_decisions (counterpart_id) WHERE counterpart_id IS NOT NULL;
-- A decision is challenged by at most one unclosed appeal.
CREATE UNIQUE INDEX idx_appeal_decisions_active
    ON appeal_decisions (kind, COALESCE(report_id, warning_id, review_id, credit_entry_id))
    WHERE active;

-- Replies from the participant an administrator asked to respond. Append-only.
CREATE TABLE appeal_responses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    appeal_id    UUID NOT NULL REFERENCES appeals (id) ON DELETE CASCADE,
    responder_id UUID NOT NULL,
    body         TEXT NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_appeal_responses_appeal ON appeal_responses (appeal_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS appeal_responses;
DROP TABLE IF EXISTS appeal_decisions;
DROP TABLE IF EXISTS appeals;
