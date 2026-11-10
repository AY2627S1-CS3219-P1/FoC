-- Restoring a removed review after an overturned appeal (RP12.3). Each
-- restoration answers exactly one removal, so a restored review may be removed
-- again later and review_removals keeps one row per removal.
-- +goose Up
ALTER TABLE review_removals DROP CONSTRAINT review_removals_review_id_key;
CREATE INDEX idx_review_removals_review ON review_removals (review_id, created_at DESC);

CREATE TABLE review_restorations (
    id         UUID PRIMARY KEY,
    removal_id UUID NOT NULL UNIQUE REFERENCES review_removals (id) ON DELETE CASCADE,
    review_id  UUID NOT NULL REFERENCES reviews (id) ON DELETE CASCADE,
    appeal_id  UUID REFERENCES user_appeals (id) ON DELETE SET NULL,
    reason     TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX idx_review_restorations_review ON review_restorations (review_id);

-- +goose Down
DROP TABLE IF EXISTS review_restorations;
DROP INDEX IF EXISTS idx_review_removals_review;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM review_removals GROUP BY review_id HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'cannot restore review_removals_review_id_key: a review has several removals';
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE review_removals ADD CONSTRAINT review_removals_review_id_key UNIQUE (review_id);
