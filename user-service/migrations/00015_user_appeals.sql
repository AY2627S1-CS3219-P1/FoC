-- Appeals (RP7-RP12). The affected user appeals one or more final decisions:
-- a closed report's outcome, an account warning, a review removal, or a
-- decision to keep a reported review. An administrator reviews the appeal, may
-- ask another affected participant to respond, and closes it with an upheld or
-- overturned outcome for every decision. Overturned outcomes are applied in the
-- closing transaction.
--
-- Lifecycle: open -> under_review <-> awaiting_response -> closed, as for
-- user_reports. request_id has no foreign key because the order service owns
-- orders.
-- +goose Up
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

-- +goose Down
ALTER TABLE account_warnings DROP CONSTRAINT IF EXISTS account_warnings_appeal_id_fkey;
DROP TABLE IF EXISTS user_appeal_responses;
DROP TABLE IF EXISTS user_appeal_decisions;
DROP TABLE IF EXISTS user_appeals;
