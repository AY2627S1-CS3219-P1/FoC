-- +goose Up
-- Read-only account-deletion drain checks probe by participant. The existing
-- offers/attempts indexes are order-leading, so add courier- and
-- requester-leading indexes for the per-user EXISTS probes.
CREATE INDEX offers_courier_status ON offers (courier_id, status);
CREATE INDEX creation_workflows_requester ON order_creation_workflows (requester_id, phase);
CREATE INDEX assignment_attempts_courier ON order_assignment_attempts (courier_id, phase);

-- +goose Down
DROP INDEX IF EXISTS assignment_attempts_courier;
DROP INDEX IF EXISTS creation_workflows_requester;
DROP INDEX IF EXISTS offers_courier_status;
