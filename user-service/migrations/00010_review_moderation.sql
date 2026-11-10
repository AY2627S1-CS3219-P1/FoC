-- Review content moderation. New reviews start as pending_moderation and are
-- published only when the content moderation service reports an approved
-- outcome; rejected and moderation-error reviews stay unpublished. Historical
-- rows keep the 'approved' default so existing reviews remain visible without
-- moderation jobs, and their moderation_request_id stays NULL.
-- +goose Up
ALTER TABLE reviews
    ADD COLUMN moderation_status TEXT NOT NULL DEFAULT 'approved'
        CHECK (moderation_status IN ('pending_moderation', 'approved', 'rejected', 'moderation_error')),
    ADD COLUMN content_revision INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN moderation_request_id UUID,
    ADD COLUMN applied_run_generation INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN policy_version TEXT,
    ADD COLUMN reason_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN moderated_at TIMESTAMPTZ;

-- The unpublished list reads a reviewer's rows by status; approved-only lists
-- and the reviewee aggregate keep using idx_reviews_reviewee_id.
CREATE INDEX idx_reviews_reviewer_moderation
    ON reviews (reviewer_id, moderation_status, created_at DESC, id);

-- Transactional outbox for users.review_submitted. The payload column holds
-- the complete ReviewSubmitted event as JSON; the relay publishes it after the
-- review row commits. Mirrors user_created_outbox (see 00007).
CREATE TABLE review_submitted_outbox (
    event_id        UUID        PRIMARY KEY,
    review_id       UUID        NOT NULL,
    payload_version INTEGER     NOT NULL CHECK (payload_version = 1),
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    available_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at    TIMESTAMPTZ,
    lease_token     UUID,
    lease_until     TIMESTAMPTZ,
    CONSTRAINT review_submitted_outbox_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);

CREATE INDEX idx_review_submitted_outbox_pending
    ON review_submitted_outbox (available_at, created_at, event_id)
    WHERE published_at IS NULL;

-- Dedup record for moderation.review_moderation_finished. One row per applied
-- or decided completion event, so at-least-once delivery cannot double-apply.
CREATE TABLE review_moderation_processed_events (
    event_id     UUID        PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE review_moderation_processed_events;
DROP TABLE review_submitted_outbox;
DROP INDEX idx_reviews_reviewer_moderation;
ALTER TABLE reviews
    DROP COLUMN moderated_at,
    DROP COLUMN reason_codes,
    DROP COLUMN policy_version,
    DROP COLUMN applied_run_generation,
    DROP COLUMN moderation_request_id,
    DROP COLUMN content_revision,
    DROP COLUMN moderation_status;
