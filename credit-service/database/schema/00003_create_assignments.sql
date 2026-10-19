-- +goose Up
-- Order sends a fresh assignment ID for every acceptance. An APPLIED
-- assignment set the hold to offer_amount, and reverting it restores
-- previous_amount. REVERTED is permanent.
--
-- A REVERTED row without a courier is a tombstone: Order reverted an
-- assignment that Credit had not applied, so a later Adjust for it must fail.
CREATE TABLE assignments (
    order_id        UUID        NOT NULL REFERENCES reservations (order_id),
    assignment_id   UUID        NOT NULL,
    courier_id      UUID,
    previous_amount BIGINT,
    offer_amount    BIGINT,
    status          TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reverted_at     TIMESTAMPTZ,
    PRIMARY KEY (order_id, assignment_id),
    CONSTRAINT assignments_status_check CHECK (status IN ('APPLIED', 'REVERTED')),
    CONSTRAINT assignments_reverted_check CHECK ((status = 'REVERTED') = (reverted_at IS NOT NULL)),
    CONSTRAINT assignments_applied_check CHECK (
        (courier_id IS NULL AND previous_amount IS NULL AND offer_amount IS NULL AND status = 'REVERTED')
        OR (courier_id IS NOT NULL AND previous_amount IS NOT NULL AND offer_amount IS NOT NULL
            AND previous_amount > 0 AND offer_amount > 0)
    )
);

-- At most one applied assignment per order (ADJUSTMENT_PENDING).
CREATE UNIQUE INDEX assignments_one_applied_idx ON assignments (order_id) WHERE status = 'APPLIED';

ALTER TABLE reservations ADD CONSTRAINT reservations_assignment_fkey
    FOREIGN KEY (order_id, assignment_id) REFERENCES assignments (order_id, assignment_id);

-- +goose StatementBegin
CREATE FUNCTION reject_reverted_assignment_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'assignments cannot be deleted';
    END IF;
    IF OLD.status = 'REVERTED' THEN
        RAISE EXCEPTION 'assignment % is reverted and cannot change', OLD.assignment_id;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER assignments_reverted_immutable
    BEFORE UPDATE OR DELETE ON assignments
    FOR EACH ROW EXECUTE FUNCTION reject_reverted_assignment_change();

-- +goose Down
ALTER TABLE reservations DROP CONSTRAINT reservations_assignment_fkey;
DROP TABLE assignments;
DROP FUNCTION reject_reverted_assignment_change();
