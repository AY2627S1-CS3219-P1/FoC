# ADR: Reservation RPCs and event-driven settlement

Date: 17 Oct 2026
Status: selected Order design; renewed Credit acknowledgement pending in #136

Order's requirements (R1.1.9 and NFR-20) and original #123 contracts require a
confirmed reservation before opening, followed by asynchronous terminal settlement.
This replaces the withdrawn #145/#146 RPC-settlement proposal. Foundation #135
is retained. The source PRD PDF is absent; the checked-in service requirements
are the source used for this implementation.

Order calls Reserve/GetReservation during creation, AdjustReservation during
assignment and RevertAdjustment for assignment compensation. ReleaseReservation
is only creation compensation when an Order never opened. Credit consumes
`order.outcome` to pay or return funds for Completed, Cancelled, Expired and Failed.
Order never calls SettleReservation; its draft method is deprecated and must not
be authorized for Order. No opened Order is released by RPC. Credit serializes
compensation, reservation mutations and event settlement against one reservation.
Credit owns reward/lateness/refund calculation and the PRD's 100-credit initial
grant. This change does not choose a new lateness formula or grant policy.

Credit keeps `request_id` (the permanent Order UUID) and `offer_id` field names.
Existing creation/offer retry keys remain. AcceptOrder is an explicitly approved
exception to the original keyless mutation design: it requires a client-generated
acceptance-attempt UUID. Other mutation requests do not gain required keys.
Creation deadline checks run after retained-key lookup, since an identical retry
after expiry must still find the original operation.

## Acceptance retries (implemented in #159)

AcceptOrder requires a client-generated acceptance-attempt UUID. After a courier
withdraws and the Order reopens, the UUID distinguishes a delayed retry from an
intentional new acceptance. There is no keyless fallback.

The caller generates one UUID for an intentional acceptance attempt and reuses it
for retries, including after a timeout or lost response. Order persists the attempt
and its result so replay cannot create a second assignment after withdrawal or
unassignment. Intentional reacceptance requires a new UUID. Retain the attempt
identity and result for as long as the Order is retained, rather than applying
creation's 24-hour retry-key policy. Unresolved work remains durable until resolved.
Retry-record retention does not extend the Order's acceptance deadline.

A replay returns the recorded attempt outcome alongside the current Order state.
For example, an acceptance that succeeded before courier withdrawal remains a
successful historical attempt, but the response must also show that the courier
is no longer assigned. Replaying that attempt never assigns the courier again.
The public attempt UUID and Credit's stable offer_id serve different boundaries;
their exact mapping remains part of the assignment workflow design.

Fresh direct acceptance is admitted only while the Order is Awaiting Courier and
has not expired. A fresh key for an assigned, terminal or otherwise private Order
returns not_found, just like a nonexistent Order, and stores no attempt. Recognized
keys are resolved first and still return their historical outcome and the current
caller-authorized Order.

While an Order is public, competing couriers may each have one retained rejection
when an assignment is already pending. Further fresh keys from that courier return
failed_precondition without creating more rows. The retained rejection stays
replayable for the Order's lifetime. This preserves contention among 50 eligible
couriers while bounding rejected-key growth per caller and Order. Admission
failures have no operation identity and must not be automatically retried after
the Order changes. An intentional new acceptance always uses a new UUID.

Proposal keys retain the existing 24-hour guarantee. Before sending a proposal,
the browser saves the original amount, key and first-dispatch timestamp. It will
retry only within that window. After 24 hours, or when an older saved proposal has
no timestamp, the user must refresh offers and deliberately start a new proposal.
That new proposal can replace an existing Open offer. Financial acceptance UUIDs
keep their lifetime guarantee; this proposal limit does not apply to them.

## Publication and recovery

Kafka schemas belong to `kafka/order/events/v1`, independently of RPC types.
The merged #123 Kafka contracts are the external event boundary; obsolete RPC
event messages were removed in foundation #135. Every message uses the stable outbox event UUID and Order UUID partition key. Publication
is at least once. Revision/ordinal determine local dispatch; different topics
have no shared ordering guarantee and consumers must tolerate old duplicates.
Credit atomically records event deduplication, ledger and balance changes before
acknowledging delivery. Event IDs and financial terminality both prevent repeats;
a different event ID for the same outcome must not cause another settlement.

Order lifecycle/audit/outbox commit together. A terminal lifecycle does not imply
financial completion. Kafka downtime leaves durable pending publication. Credit
downtime leaves pending settlement. Startup and five-minute reconciliation, plus
admin alerts for unexplained mismatches, remain Credit implementation dependencies.
NFR-20 and async settlement tests stay incomplete until the real consumer passes.

Creation persists reservation intent and retries before network calls, then locks
Order and verifies workflow generation/state before applying results. Timeouts and
not_found reads are unknown results. Expiry before opening persists abort intent
and confirms release (including release-before-reserve fencing) before rejection;
Rejected emits no settlement outcome. Compensation never exists only in memory.
Existing mappings live 24 hours and are pinned while unresolved. No credential is
persisted with a workflow. Service authentication is #29's short-lived,
audience-restricted workload credential boundary, separate from user JWTs; no new
static-token issuance or verification mechanism is introduced here.

Item images use S3 signed PUTs with a signed exact length, content type and
If-None-Match condition. Uploads are immutable, owner-scoped and limited to
10,000,000 bytes; at most five attach atomically to creation. Persist object keys,
not URLs. Download grants follow Order visibility. Local tests use Floci 2.3.0
with signature verification enabled. Unattached uploads expire after 24 hours;
attached images remain with history. Minimum courier rating depends on Review
and remains unimplemented, as do later lifecycle operations and the browser UI.
