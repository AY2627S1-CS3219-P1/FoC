-- +goose Up
-- Immutable, credential-free intent for an immediate role-change handoff.
-- No delivery-state or deferred-suspension policy is stored here.
CREATE TABLE role_dispatches (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 -- Retain the original actor identity without preventing account deletion.
 admin_id uuid NOT NULL,
 from_role text NOT NULL REFERENCES roles(name),
 to_role text NOT NULL REFERENCES roles(name),
 revision bigint NOT NULL CHECK(revision > 0),
 reason text NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 2000),
 created_at timestamptz NOT NULL,
 applied_at timestamptz,
 UNIQUE(user_id,revision),
 CHECK(from_role <> to_role AND (from_role='suspended' OR to_role='suspended'))
);
CREATE UNIQUE INDEX role_dispatch_pending_user ON role_dispatches(user_id) WHERE applied_at IS NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM role_dispatches WHERE applied_at IS NULL) THEN
  RAISE EXCEPTION 'unfinished role dispatches must not be discarded';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE role_dispatches;
