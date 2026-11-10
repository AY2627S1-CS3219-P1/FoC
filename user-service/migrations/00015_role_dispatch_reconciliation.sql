-- +goose Up
-- Rejected decisions retain their immutable identity and revision. They remain
-- pending for reconciliation but are excluded from automatic dispatch recovery.
ALTER TABLE role_dispatches ADD COLUMN rejected_at timestamptz;
ALTER TABLE role_dispatches ADD CONSTRAINT role_dispatch_outcome
 CHECK (applied_at IS NULL OR rejected_at IS NULL);

-- +goose Down
-- Guard the path to migration 00007: even applied decisions are revision
-- tombstones held by Order, so dropping User history would reset its revisions.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM role_dispatches) THEN
  RAISE EXCEPTION 'role dispatch decisions and revision tombstones must not be discarded';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE role_dispatches DROP CONSTRAINT role_dispatch_outcome;
ALTER TABLE role_dispatches DROP COLUMN rejected_at;
