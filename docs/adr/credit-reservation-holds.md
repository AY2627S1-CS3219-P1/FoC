# ADR: Credit reservation holds and event settlement

Date: 20 Oct 2026
Status: accepted for the Credit internals; contract comment update pending on #147
Related: #136, PR #147 (`docs/adr/order-credit-pubsub.md`)

## Context

Credit holds a requester's credits for each order. Order calls Reserve,
AdjustReservation, RevertAdjustment, ReleaseReservation (creation compensation)
and GetReservation by RPC. Terminal payout and refund come only from Kafka
`order.outcome`. The domain package `credit-service/internal/reservation`
implements these operations. Its `ApplyOutcome` is the settlement entry point
for the `order.outcome` consumer in `credit-service/internal/events`.

## Decisions

### An accepted offer never lowers the hold

AdjustReservation rejects an `offer_amount` below `original_amount` with
invalid argument, and otherwise sets the hold to `offer_amount`. Order accepts
only offers at or above the order's reward, and its client expects
`reserved_amount` to equal `offer_amount` once Assigned (credit.v1 contract).

If a lower offer shrank the hold, the requester could spend the freed credits
on another order. A later RevertAdjustment would then need to restore more than
the requester holds. That would violate `available >= 0` (C1.1.2), and the
schema cannot store a rejected revert. Because an offer only raises the hold, a
revert only ever lowers it, so it always succeeds.

Settlement pays from `offer_amount`. Lateness applies to the offer, and
everything above the payout returns to the requester. The requester's final
cost therefore still equals the courier's payout (C1.2.6). The increase above
the original is checked against available credits when the offer is accepted
(C1.2.2).

### The payout rule is injectable

Credit owns the lateness calculation today (`docs/adr/order-credit-pubsub.md`).
Settlement asks a `reservation.PayoutRule` how much of the accepted offer the
courier is paid; `LatenessPayout` applies the lateness rule and is injected
from the composition root. Settlement rejects any payout below zero or above
the hold with `ErrOutcomeMismatch` and changes nothing.

If Order later computes the charge (agreed on #136), add the payable amount to
`order.outcome`, carry it on `OrderOutcome`, and inject a rule that returns it.
Settlement, the ledger and the schema do not change.

### Event settlement relies on terminality, not an event table

Credit records no consumed `event_id`. Settled and Released reservations never
change (enforced by trigger), so a redelivered or repeated outcome finds a
terminal reservation and changes nothing. This holds even when the repeat has
a different event ID. `ApplyOutcome` reports these as `applied = false`. The
consumer can acknowledge an event once `ApplyOutcome` returns, because the
money, ledger and reservation changes commit in one transaction.

Some outcomes contradict Credit's records:
- a different terminal outcome;
- a payout for a Held or unknown reservation;
- a requester or courier mismatch.

These return `ErrOutcomeMismatch`, `ErrReservationNotAssigned` or
`ErrReservationNotFound` and change nothing. Reconciliation and admin alerts
(NFR-09.5–09.7, NFR-10.4) remain separate work and are expected to use these
errors.

A release outcome for an unknown order leaves a tombstone, like
ReleaseReservation, so a delayed Reserve cannot hold credits afterwards.

### The consumer acknowledges mismatched outcomes

The `order.outcome` consumer (`internal/events`) logs a mismatch at error
level and acknowledges it. A mismatch is any of the errors above, or an
invalid payload. The log carries the event ID, order ID and offset.
Redelivering a mismatch cannot change its result, and `pkg/kafka` stops the
subscription on a handler error, so failing would block every later outcome
on that partition. Reconciliation is expected to find these orders by
comparing Order's terminal orders with reservations that are still held.

`ErrAborted` is retried in place a few times. Any other error is returned:
the subscription stops without committing and restarts with backoff, so the
event is redelivered. The `users.create` consumer opens accounts the same
way. The grant is posted only when the account row is created, so a
redelivered event grants nothing.

### Serialization

Every mutation first takes a transaction-scoped advisory lock on the order ID.
This also serializes calls for orders Credit has not stored yet. Each mutation
then locks the reservation, reads the assignment in a separate statement (see
`LANDMINES/locked-row-relationship-snapshots.md`), and locks accounts in user ID
order. Every account change goes through `account.Post`, which updates the
account and appends its ledger entry together.
