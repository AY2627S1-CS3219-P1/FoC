-- Reviews and administrative removal audits. A review lets one participant of a
-- Completed request rate the other; removing a review is a soft delete that
-- marks deleted_at and records an audit row on review_removals. order_id has no
-- foreign key because the order service owns orders. Review stats live on users
-- as review_count / avg_rating.
-- +goose Up
ALTER TABLE users
    ADD COLUMN review_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN avg_rating   DOUBLE PRECISION NOT NULL DEFAULT 0;

CREATE TABLE reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deleted_at  TIMESTAMPTZ,
    reviewer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reviewee_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    order_id    UUID NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment     TEXT NOT NULL CHECK (char_length(comment) BETWEEN 1 AND 100000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A participant gives and receives at most one review per request.
CREATE UNIQUE INDEX idx_reviews_reviewer_order ON reviews (reviewer_id, order_id);
CREATE UNIQUE INDEX idx_reviews_reviewee_order ON reviews (reviewee_id, order_id);
-- Aggregation and listing for a reviewee run against this index.
CREATE INDEX idx_reviews_reviewee_id ON reviews (reviewee_id);

-- Removal audit rows. One row per removed review: review_id references the
-- (soft-deleted, retained) review, so the audit survives as long as the review
-- row does. The surrogate id stays the primary key in preparation for permanent
-- deletion, when the review row will be removed but the audit kept.
CREATE TABLE review_removals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id   UUID NOT NULL UNIQUE REFERENCES reviews (id),
    reviewer_id UUID NOT NULL,
    reviewee_id UUID NOT NULL,
    order_id    UUID NOT NULL,
    reason      TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 100000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE review_removals;
DROP TABLE reviews;
ALTER TABLE users
    DROP COLUMN review_count,
    DROP COLUMN avg_rating;
