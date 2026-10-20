# ADR: Order creation, image attachment and outbox execution

Date: 17 Oct 2026
Status: implemented on the replacement execution branch; pending review
Foundation: #135. Integration: #136 and the reservation/pubsub ADR in #147.

Creation retains only the durable mechanics needed for the original split:
canonical caller/method/client-key lookup, permanent Order reservation identity,
Supplier snapshots, local transaction/audit/outbox, unknown-result reconciliation
and confirmed creation compensation. The original Credit `request_id` wire name
is preserved. No terminal settlement RPC is called. Five unrelated mutation
methods retain their original keyless request shapes.

Migration 00002 defines creation workflows and the shared
`order_idempotency_keys` table directly. Keys have a generic `resource_id` without
a resource FK, a required `request_hash`, and no CreateOrder-only method check.
The primary key is `(caller_id, method, idempotency_key)`. Migration 00003 adds
owner-scoped image records and expiring outbox claims.
Creation points to its workflow; future MakeOffer points to its offer, using the
same transactional key lookup/insert/delete adapter and payload comparison.

Key rows contain retry metadata only. Canonical creation payload, progress and
result stay in the workflow table. The existing immutable workflow hash remains
an integrity baseline; payload comparison uses the hash on the key row. A lookup
reads the key before resolving its resource, so a missing or inconsistent
resource fails internally instead of executing again, even when the key expired.
The resource and key must be inserted in one owning-operation transaction.
Caller/method advisory locks serialize duplicate admissions without conflating
methods. Generic key lookup performs no expiry cleanup: only the operation that
has resolved its resource may retire its mapping.

Resolved creation mappings may retire after 24 hours; unresolved ones remain
pinned. Successful responses describe the original creation; GetOrder describes
current lifecycle. There is no default-off admission flag. Production creation
requires the actual Credit backend and #29 workload identity, not a server fake.

Image grants last ten minutes. Exact Content-Length, Content-Type and
If-None-Match are signed. Client-settable signed headers are returned separately;
browsers set length themselves. Objects cannot be overwritten. Creation inspects
metadata outside the database transaction, then rechecks owner and unattached
state while atomically attaching up to five images. The body is not decoded or
converted; media validation here checks the declared supported type and stored
metadata. The limit is 10,000,000 bytes (decimal MB). Read authorization precedes
five-minute download grants. Cleanup marks old unattached rows as deleting before
remote deletion, fencing a concurrent attachment; deletion is idempotent and
retried. Attached rows are retained with Order history.

The relay uses stable event IDs and immutable version-one facts. Audit supplies
actor identity and the immutable Order snapshot supplies pickup name absent from
older fact payloads. Claims expire after 30 seconds. Each publish has a five-second
budget; failed publication defers its row two seconds. The worker scans every
second and drains up to fifty facts. It never holds database locks over Kafka.
Only the earliest unpublished fact per Order is claimable; independent Orders
continue when another Order is deferred. A stale publisher can still deliver an
old duplicate, so revision-aware consumers and financial terminality are required.
No cross-topic ordering or exactly-once transport is claimed.

Creation uses fifteen-second leases, two-second remote calls, a five-second inline
budget, four recovery workers, and jittered retry backoff capped at thirty seconds.
These are implementation defaults, not PRD business policy. Image cleanup runs
at startup and each minute. Service cancellation stops workers before clients and
database pools close. Credit/Kafka/S3 are not probed for reachability on startup.

Shared `pkg/auth/httpauth` middleware stores both the verified caller and its
Bearer authorization header in request context after authentication. Supplier
lookups retrieve the delegated header through `auth.UserBearerFromContext`; RPC
handlers do not copy credentials from incoming headers. The credential stays
separate from `Caller`, is not persisted, and is never sent to Credit.

TokenSource is the consuming Order adapter's workload-credential boundary. The
file provider consumes externally issued JWTs, checks dispatch expiry/subject/
audience, and rereads rotations; it does not authenticate the caller to Credit.
The receiver's cryptographic verification and deployment issuance are #29 work.

Tests exercise PostgreSQL, private/signature-verifying Floci and real Kafka.
Remote Credit and Supplier are scripted test endpoints. Terminal events are
produced through the existing aggregate, since later lifecycle RPCs are outside
this slice. Real Credit settlement, five-minute financial reconciliation, admin
alerts, minimum courier rating, proof upload, later lifecycle operations and the
browser flow remain explicit dependencies. NFR-20 is partial until the actual
Credit consumer is verified.

## Browser logout and unresolved submissions

Logout clears submitted locations, instructions, description and image references
from tab storage. It retains caller-scoped retry metadata and a redaction marker
for unresolved submissions. Signing back in must not silently create a new retry
identity: recovery blocks creation and directs the caller to My Orders. The
redacted payload cannot be replayed. Starting another Order requires explicit
confirmation that the caller checked My Orders and resolved the earlier request;
this never sends a request automatically. Confirmed outcomes still clear their
saved record normally. Corrupt or expired records also fail closed.
