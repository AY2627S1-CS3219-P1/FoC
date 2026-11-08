# ADR: Account deletion lifecycle

* Status: Proposed
* Date: 2026-11-08
* Deciders: User Service owners
* Related: [issue 263](https://github.com/AY2627S1-CS3219-P1/FoC/issues/263), PR 228 (access-token revocation)

## Context

Users can request hard account deletion. The live `users` row must eventually be
physically removed, while retained history (reviews, order/credit records,
audits) stays meaningful. Deletion coordination spans databases: Order and
Credit can hold outstanding obligations, and a check-then-delete race can
admit new work between the check and the final delete.

An earlier design used durable per-user admission gates in Order and Credit
with `PrepareUserDeletion`/`AbortUserDeletion` RPCs and a multi-state
coordinator. That was replaced by the lifecycle design below because it needs
no participant write protocol, no gate tables and no distributed abort
coordination.

## Decision

### Lifecycle

`active → deleting → physically absent`. Acceptance is **irreversible**:
`users.is_deleting` commits even when outstanding work exists, and no endpoint,
worker path or administrator toggle can reset it. A `BEFORE UPDATE` trigger
rejects `true → false` and any change to `deletion_requested_at`.

### Authorization via token claims, not per-request lookups

Access tokens minted after acceptance carry an `is_deleting` claim, surfaced as
`auth.Caller.IsDeleting`. Each owning service classifies its start-work methods
and rejects deleting callers there; completion, cancellation, withdrawal,
settlement, recovery and history reads keep their normal permissions. No
per-request account/fence lookup is added to normal authenticated requests —
the verified token carries the state, and revocation remains PR 228's concern.

Legacy tokens without the claim are unrestricted by definition; finalization
therefore waits for a conservative horizon (see below) rather than trusting a
distributed revocation acknowledgement.

### Drain visibility through read-only queries

The User Service worker calls internal read-only `GetUserOutstandingWork`
operations on Order and Credit. They run one consistent snapshot per query
(single statement), never mutate state, and treat timeouts/errors as unknown —
never as empty. Any outstanding, unknown or errored result postpones
finalization without touching the flag.

### Finalization floor

```text
admissions_safe_after =
    deletion_requested_at
  + maximum lifetime of previously issued unrestricted access tokens
  + accepted verifier clock skew
  + maximum time for an authenticated start-work request to commit
```

Finalization never runs before this horizon. A best-effort revocation write
failure is not evidence that old tokens became unusable.

### Final transaction

After the horizon passes and every drain query is definitively empty, one User
Service transaction deletes sessions, user-bound magic-link tokens (including
email-bound challenges), favourites, the user row itself, and records exactly
one `users.user_deleted` outbox fact. The outbox is independent of the users
row and publishes with retry after the commit. Broker or storage failure never
rolls back a committed deletion.

### Consumers

Consumers apply only the local effects they actually need. Credit records a
minimal deleted-user marker checked where accounts are opened (including the
`users.create` consumer and lazy-open paths) so replay cannot resurrect an
account or re-grant starting credits. Communication removes Telegram links,
codes and selections. Supplier withdraws still-pending location proposals.
Order keeps no user read-model and needs no consumer. No service installs
per-request account-fence tables.

## Alternatives considered

* **Prepare/abort admission gates** (previous design): race-free, but required a
  write protocol, durable per-operation decision records and abort-coordination
  in every participant for a rare user-initiated event. Rejected by the amended
  issue.
* **Rejecting deletion while work exists**: simpler, but leaves users stuck and
  contradicts the irreversible-acceptance product decision.
* **Soft delete only**: retains a full profile behind a flag; the issue requires
  physical removal.
* **Distributed transaction / 2PC across services**: blocking and operationally
  heavy; still needs participant prepare logic.

## Consequences

* Deleting accounts stay logged in with restricted tokens and finish existing
  work; the UI hides start-work controls and shows waiting reasons.
* A user can remain `deleting` indefinitely while work is outstanding; the
  worker keeps retrying and never abandons a job.
* Retained reviews/audits keep nullable user references and render as
  "Deleted account"; historical cross-service UUIDs remain opaque IDs.
* Re-registration with the same email creates a new UUID with independent
  history; old UUIDs are never reused or relinked.
