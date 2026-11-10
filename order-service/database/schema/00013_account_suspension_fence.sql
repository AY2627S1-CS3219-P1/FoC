-- +goose Up
CREATE TABLE order_account_suspensions (
 user_id uuid PRIMARY KEY,
 decision_id uuid NOT NULL UNIQUE,
 suspension_revision bigint NOT NULL CHECK (suspension_revision > 0),
 suspended boolean NOT NULL,
 admin_id uuid NOT NULL,
 reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000)
);

-- +goose Down
-- A rollback must not silently re-enable a suspended account.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM order_account_suspensions) THEN
  RAISE EXCEPTION 'account suspension fences and revision tombstones must not be discarded';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE order_account_suspensions;
