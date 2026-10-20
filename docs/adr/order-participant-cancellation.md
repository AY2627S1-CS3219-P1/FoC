# ADR: Participant cancellation and durable withdrawal

Date: 20 Oct 2026
Status: accepted implementation policy
Dependencies: acceptance/offers #159, creation/recovery, and
[durable assignment history](order-read-privacy-and-assignment-history.md).
The source PRD PDF remains unavailable; checked-in requirements and accepted
contracts are the baseline.

Participant cancellation lives in `internal/order`, sharing the aggregate mutation
boundary, audit, outbox relay and central assignment-history writer. Authorization
uses persisted requester/current courier relationships. Suspension retains these
rights; an admin role alone does not grant them. Delivered, Pending Reservation
and terminal Orders reject new cancellations. Keyed historical retries are resolved
before current lifecycle eligibility and return their recorded operation status
with the caller-authorized current Order, including history redaction.

`CancelOrderRequest.idempotency_key` is optional. A UUID scopes to caller and RPC
method in shared idempotency storage, with a canonical order/reason hash. Trim
reason whitespace and limit it to 500 Unicode characters. Keep mappings for the
Order's lifetime, including SUCCEEDED and SUPERSEDED results. Changed payloads
return already_exists. Equivalent requests join a pending withdrawal and may bind
additional keys to it. A different reason on a new request during withdrawal
returns aborted rather than overwriting the durable intent.

Keyless calls retain current-state semantics. A keyless call after terminal
cancellation fails eligibility; after withdrawal it has no participant rights
unless a later assignment exists. A delayed keyless call cannot distinguish a
new intentional cancellation of that later assignment. Clients must not
automatically retry keyless calls after uncertainty. A new acceptance UUID permits
intentional reacceptance; old acceptance/cancellation identities never operate on
a later assignment. Historical offers remain historical and expiry is unchanged.

Before-pickup courier withdrawal first persists a PENDING operation and the
Order's pending reference, with audit only. The successful assignment attempt's
immutable identity (Credit offer_id), original amount, courier and assignment time
are captured under the Order lock. Missing identity leaves durable pending work;
never invent a reversal identity from an offer or accept a Held read as proof of
acknowledged reversal. The Order remains Awaiting Pickup, exposes
`cancellation_pending`, and blocks pickup at the shared transition boundary.

Recovery runs immediately at startup and every second, with bounded calls,
backoff, batches and lease/generation fencing shared with assignment patterns.
Call RevertAdjustment outside transactions, require acknowledgement, then read a
matching Held reservation at the original amount with no assignment. Released,
wrong-amount, missing and uncertain observations remain pending. Re-lock the
Order and verify operation, generation, lease, current courier, assignment time,
and status before applying. Restore the original reward/reservation, clear the
current assignment/pending reference, close history through the central writer,
and emit one status fact. No public offer is reactivated.

Requester cancellation supersedes pending withdrawal, clears its pending
reference and closes immediately. Any unfinished acceptance is moved to durable
compensation with an advanced generation in that same transaction. Late workers
cannot assign or reopen a terminal Order. Terminal cancellation preserves current
participants, pickup timestamps, reservation observations and other snapshots.
Requester cancellation yields Cancelled; courier cancellation in Delivering yields
Failed. All terminal paths emit exactly one status and outcome; the latter courier
path also emits a warning request. Settlement remains exclusively event-driven;
cancellation never calls ReleaseReservation or SettleReservation. Item-location
recording, frontend controls, admin operations and deadline workers are separate.

Migration 00006 is additive and reversible. Cancellation records, aggregate,
assignment compensation, history, audit, keys and facts commit atomically. Workflow
lease changes without aggregate changes carry no new lifecycle audit/event.
Outstanding cancellations require configured Credit workload credentials at startup
but Credit reachability is not a startup dependency.

Verification uses disposable PostgreSQL, mounted generated Connect clients,
scripted Credit and the existing Kafka publication harness. Passing those checks
does not certify real Credit adjustment/reversal/settlement, permanent remote
idempotency fencing or workload authorization. Deployment verification against the
real Credit service and its outcome consumer remains an external dependency.
