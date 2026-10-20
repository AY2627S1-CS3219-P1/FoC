-- +goose Up
-- One reservation per order, keyed by the Order Service order ID. HELD and
-- ASSIGNED hold credits. RELEASED and SETTLED are terminal and never change
-- again, so an order's credits are paid or returned at most once (C1.2.14).
--
-- A RELEASED row without a requester is a tombstone: Order released an order
-- that Credit had not seen, so a later Reserve for it must fail.
CREATE TABLE reservations (
    order_id              UUID        PRIMARY KEY,
    requester_id          UUID        REFERENCES accounts (user_id),
    status                TEXT        NOT NULL,
    original_amount       BIGINT      NOT NULL,
    reserved_amount       BIGINT      NOT NULL,
    -- The applied assignment while ASSIGNED, and the paid one once SETTLED.
    assignment_id         UUID,
    release_reason        TEXT,
    settlement_outcome    TEXT,
    paid_to_courier       BIGINT,
    returned_to_requester BIGINT,
    -- Lateness inputs of a COMPLETED settlement, kept for audit.
    picked_up_at          TIMESTAMPTZ,
    delivered_at          TIMESTAMPTZ,
    delivery_duration     INTERVAL,
    settled_at            TIMESTAMPTZ,
    revision              BIGINT      NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reservations_status_check CHECK (status IN ('HELD', 'ASSIGNED', 'RELEASED', 'SETTLED')),
    CONSTRAINT reservations_amounts_check CHECK (original_amount >= 0 AND reserved_amount >= 0),
    CONSTRAINT reservations_revision_check CHECK (revision > 0),
    CONSTRAINT reservations_tombstone_check CHECK (
        requester_id IS NOT NULL OR (status = 'RELEASED' AND original_amount = 0)
    ),
    -- Adjust starts only from HELD and Revert restores the previous hold, so a
    -- HELD reservation always holds its original amount.
    CONSTRAINT reservations_held_check CHECK (
        status <> 'HELD' OR (original_amount > 0 AND reserved_amount = original_amount AND assignment_id IS NULL)
    ),
    CONSTRAINT reservations_assigned_check CHECK (
        status <> 'ASSIGNED' OR (reserved_amount > 0 AND assignment_id IS NOT NULL)
    ),
    CONSTRAINT reservations_terminal_check CHECK (
        CASE WHEN status IN ('RELEASED', 'SETTLED')
            THEN reserved_amount = 0
                AND settlement_outcome IS NOT NULL
                AND paid_to_courier IS NOT NULL AND paid_to_courier >= 0
                AND returned_to_requester IS NOT NULL AND returned_to_requester >= 0
                AND settled_at IS NOT NULL
            ELSE settlement_outcome IS NULL
                AND paid_to_courier IS NULL
                AND returned_to_requester IS NULL
                AND settled_at IS NULL
        END
    ),
    CONSTRAINT reservations_released_check CHECK (
        (status = 'RELEASED') = (release_reason IS NOT NULL)
        AND (status <> 'RELEASED' OR (settlement_outcome = 'RELEASED' AND paid_to_courier = 0))
    ),
    CONSTRAINT reservations_release_reason_check CHECK (
        release_reason IN ('CANCELLED_BEFORE_PICKUP', 'EXPIRED', 'FAILED')
    ),
    CONSTRAINT reservations_settled_check CHECK (
        status <> 'SETTLED' OR (
            settlement_outcome IN ('COMPLETED', 'CANCELLED_AFTER_PICKUP') AND assignment_id IS NOT NULL
        )
    ),
    CONSTRAINT reservations_completion_check CHECK (
        CASE WHEN settlement_outcome IS NOT DISTINCT FROM 'COMPLETED'
            THEN picked_up_at IS NOT NULL AND delivered_at IS NOT NULL AND delivery_duration IS NOT NULL
                AND delivered_at >= picked_up_at AND delivery_duration > INTERVAL '0'
            ELSE picked_up_at IS NULL AND delivered_at IS NULL AND delivery_duration IS NULL
        END
    )
);

-- Active holds per requester, for checking accounts.reserved.
CREATE INDEX reservations_active_requester_idx ON reservations (requester_id)
    WHERE status IN ('HELD', 'ASSIGNED');

-- +goose StatementBegin
CREATE FUNCTION reject_terminal_reservation_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'reservations cannot be deleted';
    END IF;
    IF OLD.status IN ('RELEASED', 'SETTLED') THEN
        RAISE EXCEPTION 'reservation % is % and cannot change', OLD.order_id, OLD.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER reservations_terminal_immutable
    BEFORE UPDATE OR DELETE ON reservations
    FOR EACH ROW EXECUTE FUNCTION reject_terminal_reservation_change();

-- Each UPDATE produces the next revision, which reservation_revisions records.
CREATE TRIGGER reservations_bump_revision
    BEFORE UPDATE ON reservations
    FOR EACH ROW EXECUTE FUNCTION bump_revision();

-- Every revision of every reservation. A replayed RPC returns the revision
-- its first call produced, not the current state. Columns mirror
-- reservations; add any new reservations column here too.
CREATE TABLE reservation_revisions (
    LIKE reservations,
    PRIMARY KEY (order_id, revision)
);

-- +goose StatementBegin
CREATE FUNCTION record_reservation_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO reservation_revisions SELECT NEW.*;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER reservations_record_revision
    AFTER INSERT OR UPDATE ON reservations
    FOR EACH ROW EXECUTE FUNCTION record_reservation_revision();

CREATE TRIGGER reservation_revisions_append_only
    BEFORE UPDATE OR DELETE ON reservation_revisions
    FOR EACH ROW EXECUTE FUNCTION reject_append_only_change();

-- +goose Down
DROP TABLE reservation_revisions;
DROP TABLE reservations;
DROP FUNCTION record_reservation_revision();
DROP FUNCTION reject_terminal_reservation_change();
