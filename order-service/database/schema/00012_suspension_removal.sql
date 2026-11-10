-- +goose Up
ALTER TABLE order_cancellations ADD COLUMN suspension_decision_id uuid;
ALTER TABLE order_cancellations ADD CONSTRAINT suspension_removal_assignment CHECK (
 suspension_decision_id IS NULL OR (
  assignment_id IS NOT NULL AND courier_id IS NOT NULL AND courier_id = caller_id AND assigned_at IS NOT NULL AND original_amount > 0
 )
);
CREATE UNIQUE INDEX suspension_removal_decision_assignment
 ON order_cancellations(suspension_decision_id,assignment_id)
 WHERE suspension_decision_id IS NOT NULL;

-- +goose Down
-- Recovery must finish before removing the identity that distinguishes automatic
-- removal from participant withdrawal. Never silently downgrade pending work.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM order_cancellations WHERE suspension_decision_id IS NOT NULL AND status = 'pending') THEN
  RAISE EXCEPTION 'pending suspension removals must finish before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX suspension_removal_decision_assignment;
ALTER TABLE order_cancellations DROP CONSTRAINT suspension_removal_assignment;
ALTER TABLE order_cancellations DROP COLUMN suspension_decision_id;
