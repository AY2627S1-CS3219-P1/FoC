# ADR: Order orchestrates Credit reservation and settlement RPCs

Date: 16 Oct 2026
Status: implemented as Order's integration baseline; Credit owner acknowledged in [#136](https://github.com/AY2627S1-CS3219-P1/FoC/issues/136)
Owners: Yihao (Order), Xiong Kai (Credit)
Contracts: Order [#123](https://github.com/AY2627S1-CS3219-P1/FoC/pull/123), Credit [#129](https://github.com/AY2627S1-CS3219-P1/FoC/pull/129)
Foundation: [Order aggregate ADR](order-aggregate-foundation.md), [#135](https://github.com/AY2627S1-CS3219-P1/FoC/pull/135)

## Context and decision

The draft contracts disagree about who initiates settlement. Select one authority:
Order orchestrates `Reserve`, `AdjustReservation`, `RevertAdjustment`,
`ReleaseReservation` and `SettleReservation`. Credit never moves money in response
to `OrderOutcome`. Order continues persisting lifecycle and outcome facts for
notifications, audit and other consumers. Credit calculates payable rewards,
lateness deductions and refunds; Order supplies amounts and lifecycle observations.

RPCs expose typed outcomes directly to orchestration, while durable Order intent
allows retries after outages. This choice requires persistent workflows and
recovery; a synchronous call alone is insufficient. No network call runs inside
an Order transaction. An event-driven settlement authority would also be possible,
but enabling both authorities would permit contradictory release/payout commands.

This supersedes the settlement-transport question in the foundation ADR. It is
Order's selected proposal, not evidence of Credit owner agreement or a deployed
integration. Contract changes must land and both owners must acknowledge #136
before live saga work proceeds against this boundary.

## Durable identities and client retries

One permanent Order UUID identifies a reservation. Credit's draft `request_id`
becomes `order_id` throughout requests, reservations and ledger entries. Every
assignment attempt has a fresh durable UUID, including direct acceptance at the
current reward. Draft `offer_id` becomes `assignment_id`. Order separately stores
the originating counteroffer ID, if any. Compensation never permits reuse of an
assignment UUID. Keep all existing protobuf field numbers and types, including
`offer_amount`, which also carries the reward for direct acceptance.

`CreateOrder` and `MakeOffer` already require UUID client keys. Add required UUID
`idempotency_key` fields to `AcceptOrder` and `AcceptOffer` at field 2, and
`CancelOrder`, `AdminCancelOrder` and `UnassignCourier` at field 3. Keys are scoped
by authenticated caller and fully qualified method for 24 hours from admission.
Identical canonical payloads join/resume/replay the same workflow. Changed payloads
return `already_exists`; concurrent duplicates must not allocate multiple Order
or assignment identities. Keep the mapping while its workflow is unresolved,
even beyond 24 hours. A resolved mapping may be retired only after the window.

Canonicalization belongs to the operation and must be versioned with persisted
intent. Compare typed business data rather than transport bytes: normalize UUIDs,
use typed timestamps/durations, preserve repeated-field order and optional
presence, and apply the operation's documented text normalization consistently.
Exclude transport metadata and the retry key from the payload comparison. Persist
the canonical payload and stable remote-call data before dispatch; do not
reconstruct them from mutable resource state during a retry.

Credit permanently deduplicates by `(method, order_id)`, or
`(method, order_id, assignment_id)` for adjust/revert. It persists successful
outcomes and definitive business rejections, including insufficient credits.
Identical data returns the original outcome; changed data returns
`already_exists` with `IDEMPOTENCY_CONFLICT`. Concurrent execution must atomically
serialize deduplication, balance changes and the stored outcome. Authentication,
malformed requests and transient infrastructure failures are not stored as
business rejections. A deliberate retry after receiving more credits needs a new
client key and an eligible resource: new Order after rejected creation, or a new
assignment attempt on an eligible Order after rejected adjustment.

A successful replay is historical. It does not prove funds remain held after
release or compensation, or that an assignment still exists. `GetReservation`
returns current financial state; `GetOrder` returns current lifecycle state.
Neither historical responses nor an observation alone may authorize a new local
transition without checking the persisted workflow.

## Unknown results, compensation and terminal actions

A timeout is an unknown result. Before expiry, reconcile or retry with the same
identity and immutable data. `GetReservation: not_found` only describes that read;
an in-flight Reserve may still commit. It is never sufficient evidence to abandon
or recreate a reservation under a new ID.

Release-before-reserve creates a durable Released tombstone. Revert-before-adjust
creates a durable tombstone for that assignment, including when no reservation
exists yet. Credit serializes these fences against their corresponding mutations.
A delayed first execution cannot create a released hold or apply a reverted
assignment. A previously successful call can still replay its stored historical
success, without performing another mutation.

Every response is applied only after re-locking Order, reading relationships in
a subsequent statement, and checking the persisted workflow identity and phase.
A delayed adjustment response for an obsolete attempt cannot confirm another
assignment. Compensation targets exactly its assignment generation and must not
revert a newer one. Persist compensation until confirmed; block incompatible
local actions, including pickup during withdrawal/unassignment, and do not start
another assignment until required compensation completes.

Credit serializes terminal actions with all reservation mutations. Repeated
identical release or settlement has no further effect. Changed data for the same
operation conflicts; release versus settlement has one winner and the loser
receives the typed terminal-state rejection. Neither can move money twice.
Reports and appeals do not reopen terminal reservations.

## Lifecycle mapping

| Order situation | Credit action and local result |
| --- | --- |
| Creation reservation confirmed before expiry | `Reserve`; re-lock and open as Awaiting Courier only if the same workflow is still eligible and unexpired. |
| Reservation definitively refused | Reject creation; no release call or settlement outcome fact. |
| Reservation result unknown before expiry | Remain Pending Reservation; reconcile/retry the same Reserve. |
| Creation expires before opening | Persist abort intent, then `ReleaseReservation(EXPIRED)`, including an unknown reservation. Reject only after confirmed release. Never open the expired Order. |
| Assignment succeeds | `AdjustReservation` using a new assignment UUID; confirm local assignment only after success and workflow recheck. |
| Adjustment definitively fails without financial effect | Preserve the existing hold and leave Order Awaiting Courier. |
| Adjustment succeeds but local assignment cannot complete | Durably `RevertAdjustment` for that assignment before another attempt. |
| Courier withdrawal or admin unassignment | Revert the active assignment before reopening; use Credit's restored hold amount. |
| Completed | `SettleReservation(completed)` with pickup/delivery timing and allowed duration; Credit applies lateness rules. |
| Expired after opening | `ReleaseReservation(EXPIRED)`. |
| Requester/admin cancellation before pickup | `ReleaseReservation(CANCELLED_BEFORE_PICKUP)`. |
| Courier cancellation after pickup or delivery timeout | `ReleaseReservation(FAILED)`. |
| Requester cancellation after pickup | `SettleReservation(cancelled_after_pickup)` with the full reward. |

Terminal lifecycle changes and their concrete financial intent commit atomically
before calling Credit. Their lifecycle facts do not assert completed financial
processing. Creation abort is distinct: remain Pending Reservation with abort
intent until release is confirmed, then become Rejected without a terminal
settlement outcome. A definitive Reserve refusal can reject directly. Preserve
pickup/delivery observations and financial input in terminal intent.

## Errors and authentication

Credit business errors carry `google.rpc.ErrorInfo`, domain `credit.v1`. Branch
on reason and code, never human-readable message text.

| Reason | Connect code |
| --- | --- |
| `INSUFFICIENT_CREDITS` | `failed_precondition` |
| `IDEMPOTENCY_CONFLICT` | `already_exists` |
| `RESERVATION_NOT_FOUND` | `not_found` |
| `RESERVATION_RELEASED`, `RESERVATION_SETTLED` | `failed_precondition` |
| `RESERVATION_NOT_ASSIGNED`, `ADJUSTMENT_PENDING`, `ADJUSTMENT_REVERTED` | `failed_precondition` |

Authentication/authorization errors use `unauthenticated`/`permission_denied` and
malformed input uses `invalid_argument`. `unavailable`, `deadline_exceeded`,
`canceled` and `internal` are not definitive business refusals. A failed read does
not settle the result of an earlier mutation.

Every Credit reservation method, including `GetReservation`, requires a dedicated
Order-to-Credit bearer credential in `Authorization`, separate from user JWTs.
Credit authenticates at the HTTP boundary and authorizes Order at the Connect
method boundary. Reject missing, invalid and user credentials. Require HTTPS
outside explicit local development. Provisioning, audience restriction, expiry,
rotation, revocation and local/CI configuration belong to the concrete workload
identity implementation tracked by [#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29).
The credential must not appear in clients, logs or persisted workflow payloads.
This ADR specifies the boundary; authentication implementation and integration
tests are prerequisites for live calls.

## Creation handoff and scope

Order now implements creation and recovery against this boundary; see the
[creation/recovery ADR](order-creation-recovery.md). Credit implementation remains
separate. Creation validates shape, looks up idempotency and checks the deadline
only for a new admission. Identical retained requests replay after their deadline
without creating a second Order. Image uploads remain deferred; new creation
requires no image IDs.

Credit's owner acknowledged the baseline in #136. Live financial integration
still requires the authenticated Credit implementation and its conformance tests.
Order's creation tests cover unknown Reserve results, expiry while reserving and
new operation instances recovering after remote success/local rollback. Initial
Order, workflow, mapping, audit and status fact commit atomically. Production uses
the real generated Credit client; scripted remote outcomes are test-only.
Assignment recovery, terminal financial workflows and Kafka relay remain separate
follow-up work. [Conformance cases](../testing/order-credit-conformance.md) define
the required behavior and identify implemented Order tests versus remaining
Credit and later-lifecycle acceptance work.

## Requirements deviation

The current NFR-20 assumes pub-sub between Order and Credit for settlement. This
proposal intentionally uses Order-orchestrated RPCs for money movement and keeps
outbox lifecycle facts for other consumers. RPC settlement does **not** fulfill
NFR-20's event-driven settlement requirement. Obtain explicit requirement-owner
agreement for this deviation; do not mark NFR-20 fulfilled. Async-event-specific
NFR-11.18 coverage likewise cannot be claimed from RPC tests. Durable intent,
recovery and mismatch detection still need implementation and evidence for
NFR-04, NFR-09 and NFR-11.19.
