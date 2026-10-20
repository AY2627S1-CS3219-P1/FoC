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
for the future consumer.

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

### Serialization

Every mutation first takes a transaction-scoped advisory lock on the order ID.
This also serializes calls for orders Credit has not stored yet. Each mutation
then locks the reservation, reads the assignment in a separate statement (see
`LANDMINES/locked-row-relationship-snapshots.md`), and locks accounts in user ID
order. Every account change goes through `account.Post`, which updates the
account and appends its ledger entry together.
