-- Permanent account-deletion lifecycle. is_deleting is a one-way flag: once set
-- it can never be cleared and its timestamp can never move. Profile edits, role
-- changes, seeds and update DTOs must not clear it implicitly.
-- +goose Up
ALTER TABLE users
    ADD COLUMN is_deleting         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN deletion_requested_at TIMESTAMPTZ;

-- Both flags absent/false, or both present/true: the timestamp only exists while
-- the account is deleting.
ALTER TABLE users
    ADD CONSTRAINT users_deletion_lifecycle_check
        CHECK (
            (is_deleting = FALSE AND deletion_requested_at IS NULL)
            OR
            (is_deleting = TRUE AND deletion_requested_at IS NOT NULL)
        );

-- Reject clearing the flag (true -> false) and any change to an established
-- request time. The WHEN clause fires only when one of these two columns
-- actually changes, so unrelated updates (profile, role, updated_by) never
-- trip it.
-- +goose statementBegin
CREATE OR REPLACE FUNCTION users_deletion_irreversible() RETURNS trigger AS $$
BEGIN
    IF OLD.is_deleting IS TRUE AND NEW.is_deleting IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'user deletion is irreversible: is_deleting cannot be cleared';
    END IF;
    IF OLD.deletion_requested_at IS NOT NULL
       AND NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at THEN
        RAISE EXCEPTION 'deletion_requested_at is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER users_deletion_irreversible
    BEFORE UPDATE ON users
    FOR EACH ROW
    WHEN (OLD.is_deleting IS DISTINCT FROM NEW.is_deleting
          OR OLD.deletion_requested_at IS DISTINCT FROM NEW.deletion_requested_at)
    EXECUTE FUNCTION users_deletion_irreversible();

-- +goose Down
DROP TRIGGER IF EXISTS users_deletion_irreversible ON users;
DROP FUNCTION IF EXISTS users_deletion_irreversible();
-- The flag cannot be dropped back while the table references the trigger state;
-- only drop the columns once they are restored to their default shape.
-- +goose statementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE is_deleting = TRUE) OR
       EXISTS (SELECT 1 FROM users WHERE deletion_requested_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot downgrade is_deleting while any account is deleting';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE users DROP COLUMN deletion_requested_at;
ALTER TABLE users DROP COLUMN is_deleting;
