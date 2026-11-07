# ADR: Review content moderation

Date: 8 Nov 2026
Status: implemented for #264

## Decision

A separate `content-moderation-service` (CMS) asynchronously assesses newly
submitted review text with an LLM before publication. User Service keeps
exclusive ownership of review rows, publication, rating statistics and
administrative removal. CMS owns assessment policy, prompts, inference
integration, job execution, retries and assessment history. The two services
communicate only through Kafka events plus reviewer-scoped read RPCs; CMS
never writes to User's database and User never reads CMS's database.

### Ownership and ports

| Consumer | Consumer-defined port | Initial implementation |
| --- | --- | --- |
| CMS job engine | `jobs.Evaluator.Evaluate(ctx, EvaluationInput) (Assessment, error)` | `internal/llmpolicy` LLM review-text evaluator |
| LLM evaluator | `llmpolicy.CompletionClient.Complete(ctx, *CompletionRequest) (*CompletionResponse, error)` | `internal/openaiadapter` over `github.com/openai/openai-go/v3` |
| CMS job engine | Narrow claim/heartbeat/complete/retry repository interface | CMS PostgreSQL/sqlc repository |
| Outbox relays | Publisher port | Injected `pkg/kafka` publisher |

Ports use service-owned types, never generated SDK types. The evaluator
constructs prompts and validates model output; the transport adapter translates
provider requests/responses/errors. The OpenAI SDK dependency stays inside CMS.
The composition root (`cmd/server/main.go`) injects all dependencies; no
globals or `init` registration.

### Concurrency

Worker concurrency is **per CMS process**, configured by
`CMS_WORKER_CONCURRENCY` (default 2, positive integer). There is no distributed
semaphore, slot table or leader election; replicas scale total concurrency.
Replicas share the queue safely through atomic claims
(`FOR UPDATE SKIP LOCKED`), claim tokens, leases with heartbeats and fenced
completion/retry updates keyed on (job, run generation, claim token). Crash
recovery may repeat an external inference; exactly-once provider execution is
not claimed.

### Event contracts

Independent Kafka contracts (not the RPC `Review` types):

- `users.review_submitted` (`users.events.v1.ReviewSubmitted`), published by
  User from the **same sqlc transaction** that inserts the pending review.
  Carries a stable `event_id`, `moderation_request_id`, `review_id`,
  authenticated `reviewer_id`, `content_revision` and the complete immutable
  comment.
- `moderation.review_moderation_finished`
  (`moderation.events.v1.ReviewModerationFinished`), published by CMS from its
  transactional outbox on every terminal job transition. `outcome` is
  APPROVED, REJECTED or ERROR; ERROR means no valid verdict was obtained and
  never carries content reason codes. `run_generation` starts at 1 and
  increases only on an explicit retry of a terminal errored job, so a late
  older completion can never overwrite a newer result.

Both services validate incoming events again; a protobuf default or unknown
outcome can never approve content.

### User Service persistence and publication

A new additive migration extends reviews with `moderation_status`,
`content_revision`, `moderation_request_id` and the last applied moderation
metadata; existing rows are backfilled as approved and no jobs are created for
them. All ordinary review reads, counts and reviewee statistics filter
`moderation_status = 'approved' AND deleted_at IS NULL`. Submission inserts a
pending review plus a review-specific outbox row in one transaction; a pending
review contributes nothing to statistics and is acknowledged without depending
on Kafka or CMS availability. The completion consumer deduplicates, locks the
review, re-checks reviewer/request/revision identity, ignores superseded
generations, applies the outcome and recalculates statistics in one local
transaction before acknowledging. Administrative removal stays separate: an
approval cannot clear `deleted_at` or bypass removal, and automated rejections
never fabricate admin-removal audits.

### CMS queue semantics

Jobs are created at most once per `moderation_request_id` (the job uniqueness
is the ingestion inbox); duplicate deliveries are no-ops after confirming
immutable fields agree; undecodable-but-valid events are quarantined and
acknowledged. Retries are durable with capped exponential backoff and jitter,
an overall attempt budget, a smaller bounded budget for schema-invalid output,
and no retry storm for configuration errors (credentials, unsupported
model/parameters): these pause processing with a surfaced error class until an
admin corrects configuration. Every attempt is recorded append-only; terminal
decisions are never overwritten without trace; API keys are never stored and
raw provider bodies, prompts or hidden reasoning are never exposed.

Admin configuration uses immutable configuration versions plus an active
pointer with optimistic concurrency; already-started jobs keep their pinned
version. A missing configuration or API key never loses submissions: jobs
queue without burning attempts and setup status surfaces what is missing.

### Reviewer-scoped reads

`user.review.v1.ReviewService/ListUnpublishedReviewsForUser` is the canonical
unpublished projection (pending/rejected/moderation-error, by author, with
backend authorization before lookup). CMS's
`moderation.review.v1.ReviewModerationService/GetReviewModerationJobsForReviewer`
returns bounded job summaries for the request IDs of one User page; missing
jobs mean not-yet-ingested, never approval. User's publication status always
wins over CMS progress in the UI.

## Alternatives considered

- **Synchronous moderation in User's submit path**: couples User to inference
  latency/availability and blocks submissions; rejected.
- **In-process queue in User Service**: spreads moderation policy into User
  and mixes retry/lease machinery into a domain that owns publication;
  rejected.
- **Shared `pkg/llm` framework**: one consumer today; premature. The
  completion port inside CMS is the replaceable boundary.
- **Distributed concurrency cap**: rejected as unnecessary cluster-wide state;
  per-process limits plus replica count are explicit and sufficient.

## Consequences

- Publication latency now includes moderation time; reviewers see their
  review as pending on the new unpublished page until CMS finishes.
- Moderation outages degrade to a growing pending queue, never lost
  submissions.
- A rejected review is retained and visible to its author and admins but does
  not affect ratings; rejection is distinct from technical error and from
  admin removal.
