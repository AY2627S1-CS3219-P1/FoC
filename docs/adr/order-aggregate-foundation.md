# ADR: Order aggregate, local transactions and future orchestration

Date: 15 Oct 2026
Status: accepted
Dependencies: Order contracts [#123](https://github.com/AY2627S1-CS3219-P1/FoC/pull/123)
(`a0b1e801`), Credit contracts [#129](https://github.com/AY2627S1-CS3219-P1/FoC/pull/129)
remain under discussion. Foundation base: main `5233ef5`.

## Context and source assumptions

Order owns the lifecycle, offers, participant relationships and local pickup and
delivery snapshots. It must retain durable facts while reservation and settlement
integration is agreed. The checked-in `order-service/REQUIREMENTS.md` and latest
Order contracts were reread; the source PRD PDF is not present in the repository.
The requirements do not explicitly resolve cancellation once Delivered. The
approved default, consistent with #123, is receipt confirmation or future report
handling: Delivered has no ordinary cancellation transition. Revisit this choice
if the source material supplies a contrary requirement.

## Package and dependency boundaries

Follow Supplier's domain organization and its accepted Location structure ADR,
but retain one `internal/order` package. Creation, assignment, pickup, delivery,
completion and cancellation share an Order aggregate and transaction boundary.
Splitting each into an independent transaction stack would obscure that coupling.
Separate files group types, transition policy, reader, repository, service,
changes/events and Postgres implementations; future operations add their own files
in the same package.

Operations consume interfaces declared by Order. Pure policy and fake-backed
operation tests use domain types and no database. Colocated adapters import pgx;
this is an intentional tradeoff, not package-level infrastructure independence.
One generated sqlc package owns query bindings/models only. RPC packages mirror
public services, convert values, validate and authorize methods, and share one
error mapper. `cmd/server` constructs dependencies and owns configuration and
lifecycle. `internal/router` mounts the constructed RPC servers and applies
authentication, authorization and validation. No RPC or sqlc type is a business
model.

Pickup/delivery snapshots are copied locally and serialized independently of
protobuf. Other services' IDs are references, without cross-service joins or
foreign keys. All credits and revisions are bigint; timestamps are timestamptz.
Offers retain historical acceptances, including across reassignment.

## Local mutation boundary

Use READ COMMITTED and an Order row lock. Lock the parent first; read offers in
another statement after the lock finishes, as required by the relationship
snapshot landmine. Authorize with the persisted aggregate and fresh relations,
validate operation prerequisites, and build one change. The adapter keeps an
independent copy of its locked authorization baseline.

A change carries expected revision, resulting Order, conditional offer changes,
actor/action, audit entry and immutable, versioned domain facts. Order updates
check the expected revision in SQL and increment once. Each aggregate may be
applied only once per transaction. Order, offers, audit and outbox commit together;
any Apply failure poisons the transaction even if a callback swallows the error.
Callbacks are never automatically retried. Do not call another service or object
store while holding the database lock.

The transition function checks lifecycle and relationships only. Eligibility,
Credit results, deadlines and delivery proof remain operation prerequisites.
Repeat recognition belongs to each action: a matching destination status is not
sufficient. Recognized repeats return a nil change and write nothing. Currently,
assignment by the assigned courier and receipt by the completed Order's requester
have state-proven repeats. Same-status lifecycle writes are rejected. Terminal
item-location amendments are restricted to disposition metadata and never reopen
an Order or emit another outcome. Future offer mutations must add their concrete
actions and permitted fields to the change policy when implemented.

## Audit and outgoing facts

Every aggregate revision has one bounded audit entry. Creation writes Pending
Reservation and its initial status fact. Actual status changes write one status
fact; Completed, Cancelled, Expired and Failed also write one terminal outcome.
Rejected does not request settlement. Courier cancellation in Delivering also
writes a warning request. Metadata changes write audit without status facts.

Cancellation preserves participants, pickup/delivery observations, reserved
amount and allowed duration; outcomes distinguish before/after pickup. Credit
calculates payable rewards. Cancellation cannot erase observations to change the
meaning of its outcome. Facts also retain the previous courier on unassignment.

The outbox uses stable UUID event IDs, payload versions, and per-Order revision /
ordinal ordering. Unique constraints protect one fact of each kind per revision,
ordinals and one terminal outcome per Order. Publication timestamps are separate
from fact content. No relay exists yet. RPC events in #123 are not persistence
models, and Kafka serialization/transport belongs to a later relay.

## Future durable orchestration

Order will own durable orchestration:

1. Persist concrete intent and stable operation identity/data locally.
2. Commit before calling Credit.
3. Re-lock the Order, reread relationships, and verify the stored operation
   identity and state before applying the response.
4. Recover unfinished work, including compensation, after restart.

A timeout means the remote result is unknown. Retries reuse the same identity and
data. Compensation is durable work, not an in-memory error handler. Workflow
state and outbox delivery state are different concerns. Add concrete creation /
assignment workflow records with their operations; no generic workflow tables,
recovery worker, saga library, or Credit client is introduced now. #123 and #129
originally disagreed on synchronous settlement versus terminal event consumption.
The [Order/Credit orchestration ADR](order-credit-orchestration.md) now records
Order's RPC baseline proposal; joint acknowledgement and live implementation are
still pending in #136.

## Reads, runtime and images

List/count and Order/offer reads use REPEATABLE READ, READ ONLY snapshots without
mutation locks. Ordering is created_at DESC, id DESC. Visibility is filtered before
counting/pagination. Browsers receive eligible Awaiting Courier orders without
instructions. Requester/current-courier history survives suspension; only the
requester/admin sees all offers, while a courier sees their own historical offers.
The internal participant service remains unmounted. Mutation/upload RPCs retain
generated unimplemented handlers.

Runtime requires PostgreSQL and User Service authentication keys, with bounded
cancellation-aware startup. Embedded migrations can be disabled externally.
Credit, Kafka and image storage are not startup dependencies.

`internal/images.ObjectStorage` contains provider-independent values and methods
only. Future workflows persist application-generated stable keys, never temporary
URLs. Inspection reports observations; image workflows enforce business limits.
AWS S3 is the intended backend; evaluate Floci locally when implementing uploads.
SDK clients, credentials, buckets, signing, image persistence and upload workflows
are deferred. A configurable fake exists only in tests and is never wired into the
server or injected as an unused Order dependency.
