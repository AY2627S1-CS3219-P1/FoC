-- Reviews and administrative removal audits. A review lets one participant of a
-- Completed request rate the other; removing a review is a hard delete recorded
-- on review_removals. order_id has no foreign key because the order service owns
-- orders. Review stats live on users as review_count / avg_rating.
-- +goose Up
ALTER TABLE users
    ADD COLUMN review_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN avg_rating   DOUBLE PRECISION NOT NULL DEFAULT 0;

CREATE TABLE reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
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

CREATE TABLE review_removals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id UUID NOT NULL,
    reviewee_id UUID NOT NULL,
    order_id    UUID NOT NULL,
    reason      TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 100000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID NOT NULL REFERENCES users (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE review_removals;
DROP TABLE reviews;
ALTER TABLE users
    DROP COLUMN review_count,
    DROP COLUMN avg_rating;
