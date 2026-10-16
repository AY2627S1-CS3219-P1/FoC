# ADR: Durable Order creation and reservation recovery

Date: 16 Oct 2026
Status: accepted for Order implementation
Dependencies: [contract alignment #145](https://github.com/AY2627S1-CS3219-P1/FoC/pull/145), [integration #136](https://github.com/AY2627S1-CS3219-P1/FoC/issues/136)

## Boundary

Implement creation in the existing Order aggregate package using the shared
transaction and audit/outbox boundary. The [integration ADR](order-credit-orchestration.md)
defines Credit semantics; Credit's owner acknowledged that baseline in #136.
Order uses real generated Supplier and Credit clients. Scripted remote services
exist only in tests. Credit implementation and credential verification remain
Credit's responsibility. No user token authorizes a Credit reservation RPC.

The initial implementation accepts orders without images. New requests supplying
image IDs receive failed_precondition. Uploads, assignment, expiry after opening,
terminal financial workflows and Kafka publication remain follow-ups. A stored
creation result describes the original creation, not later lifecycle state.

## Admission and idempotency

Normalize UUIDs, trim business text and encode UTC timestamp/nanosecond duration
values in a versioned canonical JSON payload. Preserve optional text presence and
image ordering; normalize empty repeated images consistently. Exclude the retry
key and credentials. Store the SHA-256 hash and complete canonical business input.

Look up retained caller/method/key mappings before mutable eligibility checks.
Identical retries resume or replay the original operation; changed input returns
already_exists. Recheck after remote preflight and before admission so concurrent
duplicates cannot allocate separate Order or financial identities. A mapping can
expire after 24 hours only if its workflow has resolved. Workflow history remains
when an expired mapping is retired.

Fetch and validate both unarchived Location references before admission, using the
verified incoming user bearer credential only in request context. Disabled and
closed Locations remain selectable. Persist their snapshots. Recovery never
re-fetches Supplier or stores a user token.

Serialize admission per requester with a transaction-scoped advisory lock. Check
the key again, require a future deadline and admit at most ten new Orders per
rolling minute. Replays consume no quota. Preflight and invalid requests do not
create an Order. Pending Reservation, zero locally confirmed credits, creation
workflow, key mapping, audit and initial status fact commit together.

## Execution and fencing

Workflow phases are reserve_pending, abort_pending, opened and rejected. The
reservation identity is the permanent Order UUID. Amount, requester, canonical
input and deadline are immutable. Claiming increments a generation and persists
a fifteen-second lease before remote work. Always lock Order first and read the
workflow/relationships afterward in separate statements.

Use Reserve and GetReservation to confirm the current matching Held reservation;
a successful mutation response can be historical. Definitive insufficient-credit
rejection closes creation without Release or an outcome fact. Timeouts, transient
failures, malformed responses, mismatches and not_found reads keep work unresolved.

On expiry before opening, persist abort_pending before ReleaseReservation(EXPIRED),
including an unknown Reserve. Once aborting, no delayed Reserve response can open
the Order. Confirm current Released state before rejecting. When applying every
response, reacquire the Order lock and check workflow ID, phase, generation, lease,
current lifecycle state and deadline. The aggregate change and final workflow
result commit atomically. Neither rejection path emits a terminal outcome fact.

Store the original successful Order snapshot or definitive rejection for retained
retries. Read workflow and current Order under the same parent lock when building
a response. Client cancellation cannot remove committed intent; recovery retries
the same remote identity after the lease becomes available.

## Runtime and evidence

New admissions default off. Configured Credit recovery and stored-result replay
remain available with admissions off. Startup refuses to ignore outstanding work
without recovery credentials, but never probes Credit reachability. Read the
dedicated bearer token file per dispatch, supporting atomic replacement. Use
HTTPS outside local mode and refuse credential-bearing redirects.

Inline processing has a five-second budget and each remote call a two-second
budget. Recovery scans immediately and every second, processes batches of fifty
with four workers, and retries with jittered exponential backoff from one to
thirty seconds. Schedule reserve work no later than its expiry. SQL uses parent
row locks with SKIP LOCKED for claims, generation checks and the existing poisoned
transaction behavior for failed writes. Worker lifetime ends before closing the
shared database pool. Logs contain identities, phase, attempt, age and bounded
failure categories, without credentials or raw remote error messages.

The executable [creation integration tests](../../order-service/tests/creation)
use PostgreSQL 18 and mounted generated Connect clients. They exercise admission
atomicity, duplicate/rate races, expiry, stale responses, uncommitted local writes
and recovery with a new operation instance. This proves Order's behavior against
scripted remote outcomes; it does not certify Credit persistence, a complete
production process restart or all lifecycle recovery. NFR-20 remains unfulfilled:
RPC reservation/settlement does not satisfy its event-driven settlement assumption.
