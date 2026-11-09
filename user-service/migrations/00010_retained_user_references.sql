-- Retain retained-history rows after a user is hard-deleted. Live-user foreign
-- keys that previously cascaded or restricted are relaxed to nullable ON DELETE
-- SET NULL so the audit / history row survives with the reference cleared.
-- The surrogate ids and any non-FK historical identifiers stay populated.
-- +goose Up
-- Reviews keep their rows; the participants clear to NULL when either account
-- is removed. The per-request uniqueness keeps the retention guarantees intact.
ALTER TABLE reviews
    ALTER COLUMN reviewer_id DROP NOT NULL,
    ALTER COLUMN reviewee_id DROP NOT NULL;
ALTER TABLE reviews
    DROP CONSTRAINT reviews_reviewer_id_fkey,
    DROP CONSTRAINT reviews_reviewee_id_fkey;
ALTER TABLE reviews
    ADD CONSTRAINT reviews_reviewer_id_fkey
        FOREIGN KEY (reviewer_id) REFERENCES users (id) ON DELETE SET NULL,
    ADD CONSTRAINT reviews_reviewee_id_fkey
        FOREIGN KEY (reviewee_id) REFERENCES users (id) ON DELETE SET NULL;

-- RESTRICT previously prevented removing the moderator. Clear the nullable
-- reference while preserving the removal audit instead.
ALTER TABLE review_removals
    ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE review_removals
    DROP CONSTRAINT review_removals_created_by_fkey,
    ADD CONSTRAINT review_removals_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

-- Moderation history (role changes, warnings) is retained; clear the live-user
-- reference rather than cascading the whole audit row away.
ALTER TABLE role_changes
    ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE role_changes
    DROP CONSTRAINT role_changes_user_id_fkey,
    ADD CONSTRAINT role_changes_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE account_warnings
    ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE account_warnings
    DROP CONSTRAINT account_warnings_user_id_fkey,
    ADD CONSTRAINT account_warnings_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;

-- Removal snapshots can be absent when a participant was deleted before moderation.
-- Historical non-NULL opaque IDs in existing audits are retained.
ALTER TABLE review_removals
    ALTER COLUMN reviewer_id DROP NOT NULL,
    ALTER COLUMN reviewee_id DROP NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $
BEGIN
    IF EXISTS (SELECT 1 FROM review_removals WHERE reviewer_id IS NULL OR reviewee_id IS NULL) THEN
        RAISE EXCEPTION 'cannot restore removal snapshots: retained audits have absent participants';
    END IF;
END $;
-- +goose StatementEnd
ALTER TABLE review_removals
    ALTER COLUMN reviewer_id SET NOT NULL,
    ALTER COLUMN reviewee_id SET NOT NULL;

-- Restoring NOT NULL would require the retained rows to carry a live user. The
-- down migration fails explicitly instead of fabricating a dummy user.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM reviews WHERE reviewer_id IS NULL)
       OR EXISTS (SELECT 1 FROM reviews WHERE reviewee_id IS NULL) THEN
        RAISE EXCEPTION 'cannot restore reviews.participant NOT NULL: retained rows have NULL participants';
    END IF;
    IF EXISTS (SELECT 1 FROM review_removals WHERE created_by IS NULL) THEN
        RAISE EXCEPTION 'cannot restore review_removals.created_by NOT NULL: retained audits have NULL author';
    END IF;
    IF EXISTS (SELECT 1 FROM role_changes WHERE user_id IS NULL) THEN
        RAISE EXCEPTION 'cannot restore role_changes.user_id NOT NULL: retained history has NULL user';
    END IF;
    IF EXISTS (SELECT 1 FROM account_warnings WHERE user_id IS NULL) THEN
        RAISE EXCEPTION 'cannot restore account_warnings.user_id NOT NULL: retained warnings have NULL user';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE reviews
    ALTER COLUMN reviewer_id SET NOT NULL,
    ALTER COLUMN reviewee_id SET NOT NULL;
ALTER TABLE reviews
    DROP CONSTRAINT reviews_reviewer_id_fkey,
    DROP CONSTRAINT reviews_reviewee_id_fkey;
ALTER TABLE reviews
    ADD CONSTRAINT reviews_reviewer_id_fkey
        FOREIGN KEY (reviewer_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT reviews_reviewee_id_fkey
        FOREIGN KEY (reviewee_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE review_removals
    ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE review_removals
    DROP CONSTRAINT review_removals_created_by_fkey,
    ADD CONSTRAINT review_removals_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT;

ALTER TABLE role_changes
    ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE role_changes
    DROP CONSTRAINT role_changes_user_id_fkey,
    ADD CONSTRAINT role_changes_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE account_warnings
    ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE account_warnings
    DROP CONSTRAINT account_warnings_user_id_fkey,
    ADD CONSTRAINT account_warnings_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;

