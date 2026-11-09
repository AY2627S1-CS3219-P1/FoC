# ADR: Participant cancellation and durable withdrawal

Date: 20 Oct 2026
Status: accepted implementation policy
Dependencies: acceptance/offers #159, creation/recovery, and
[durable assignment history](order-read-privacy-and-assignment-history.md).

The source PRD PDF is unavailable, so we use the checked-in requirements and
accepted contracts as the baseline.

## Who can cancel

We keep cancellation in `internal/order` so it uses the existing aggregate
mutation boundary, audit, outbox relay and central assignment-history writer.
The stored requester and current courier relationships determine who can cancel.
Suspended participants keep that right. Being an admin alone does not grant it.
Pending Reservation, Delivered and terminal Orders reject new cancellations.

## Retrying a cancellation

`CancelOrderRequest.idempotency_key` is an optional UUID. We store it in the
shared idempotency storage, scoped to the caller and RPC method, alongside a
canonical hash of the order and reason. We trim whitespace from the reason and
limit it to 500 Unicode characters.

A retry with the same key returns the original operation's recorded status. We
look up that operation before checking whether the caller could cancel the Order
in its current state. The response also includes the current Order, with only the
information the caller is allowed to see.

For example, courier A withdraws, courier B accepts, and A retries the withdrawal
with the original key. A receives the result of their earlier withdrawal and the
current Order showing B's assignment. The Order follows the usual redaction rules
for former couriers. The retry cannot cancel B's assignment.

We retain key mappings for the Order's lifetime, including SUCCEEDED and
SUPERSEDED results. Reusing a key with a different order or reason returns
already_exists. Equivalent requests join an existing pending withdrawal and can
attach additional keys to it. A new request with a different reason during that
withdrawal returns aborted; the stored request stays unchanged.

Calls without a key act on the Order's current state. After terminal cancellation,
a new keyless call fails the eligibility check. After withdrawal, the former
courier has no cancellation rights unless they are assigned again. If they do
accept again, the server cannot tell whether a delayed keyless call is an old
retry or an intentional cancellation of the new assignment. Clients must not
automatically retry keyless calls when the earlier result is uncertain.

A withdrawn courier can accept again using a fresh acceptance UUID. Replaying an
old acceptance or cancellation cannot change a later assignment. Historical offers
stay unchanged, and withdrawal does not extend the Order's expiry.

## Withdrawing before pickup

We first save a PENDING cancellation and its reference on the Order. This step
writes an audit entry without emitting an event. While holding the Order lock,
we capture the successful assignment attempt's immutable identity (Credit
offer_id), original amount, courier and assignment time.

If that identity is missing, the cancellation stays pending. We must not invent
a reversal identity from an offer. A Held reservation read is also insufficient
on its own: Credit must acknowledge the reversal. Until recovery confirms it,
the Order stays Awaiting Pickup and reads expose `cancellation_pending`. The
shared transition rules block pickup during this period.

Recovery runs at startup and every second. It reuses the assignment workflow's
bounded calls, retry backoff, batches, leases and generation checks. Each attempt
calls RevertAdjustment outside the database transaction. After Credit acknowledges
the reversal, we read the reservation and require a matching Held reservation at
the original amount with no assignment. A Released reservation, wrong amount,
missing result or uncertain response leaves the cancellation pending.

Before applying the result, the worker locks the Order again and checks the
operation, generation, lease, current courier, assignment time and status. It then
restores the original reward and reserved amount, clears the current courier,
assignment time and pending reference, and returns the Order to Awaiting Courier.
The central writer closes the assignment-history entry, and the change emits one
status fact. Recovery does not reactivate any public offers.

## Closing an Order

Requester cancellation marks any pending withdrawal SUPERSEDED,
clears its pending reference and closes the Order immediately. In the same
transaction, we record any unfinished acceptance as work that needs compensation
and advance its generation. These checks prevent a late worker response from
assigning a courier or reopening the terminal Order.

Terminal cancellation keeps the current participants, pickup timestamps,
reservation observations and other snapshots. Requester cancellation produces
Cancelled; courier cancellation during Delivering produces Failed. Each terminal
cancellation emits exactly one status fact and one outcome. Courier cancellation
after pickup also emits a warning request.

Credit settles terminal Orders through events. Cancellation never calls
ReleaseReservation or SettleReservation. Item-location recording, frontend
controls, admin operations and deadline workers remain separate features.

## Storage and verification

Migration 00006 is additive and reversible. Cancellation records and the aggregate
commit in the same transaction as assignment compensation, history, audit, keys
and outbox facts. Updating a workflow lease without changing the aggregate does
not create another lifecycle audit entry or event.

If cancellations are still pending at startup, Credit workload credentials must
be configured. Credit does not have to be reachable for the service to start.

We verify this with disposable PostgreSQL, generated Connect clients against
mounted handlers, scripted Credit responses and the existing Kafka publication
harness. Those tests do not verify the real Credit service's adjustment, reversal
or settlement behavior, its permanent idempotency guarantees, or workload
authorization. Verification against Credit and its outcome consumer remains an
external deployment dependency.
