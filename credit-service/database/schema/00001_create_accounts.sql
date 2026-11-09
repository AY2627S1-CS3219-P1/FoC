-- +goose Up
-- One account per user, created with the starting credits the first time
-- Credit sees the user. balance includes credits held in reservations, and
-- available = balance - reserved must never be negative (C1.1.2).
CREATE TABLE accounts (
    user_id    UUID        PRIMARY KEY,
    balance    BIGINT      NOT NULL,
    reserved   BIGINT      NOT NULL DEFAULT 0,
    revision   BIGINT      NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_reserved_check CHECK (reserved >= 0),
    CONSTRAINT accounts_available_check CHECK (balance >= reserved),
    CONSTRAINT accounts_revision_check CHECK (revision > 0)
);

-- Rejects every UPDATE and DELETE on an append-only table.
-- +goose StatementBegin
CREATE FUNCTION reject_append_only_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;
-- +goose StatementEnd

-- Bumps revision and updated_at on every UPDATE, so writers cannot reuse a
-- revision or leave updated_at stale.
-- +goose StatementBegin
CREATE FUNCTION bump_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.revision := OLD.revision + 1;
    NEW.updated_at := CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER accounts_bump_revision
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION bump_revision();

-- +goose Down
DROP TABLE accounts;
DROP FUNCTION bump_revision();
DROP FUNCTION reject_append_only_change();
