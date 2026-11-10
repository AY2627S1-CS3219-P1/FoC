# ADR: Immediate suspension, durable handoff and Order authority

Date: 20 Oct 2026
Status: integrated with current main. Verification and deployment limits are recorded below.
Dependency: existing assignment-specific cancellation and recovery on main.

## Approved behavior and scope

An explicit admin decision with a nonblank reason suspends immediately. Warnings
never suspend automatically. Block new requests, acceptance and pickup, including
stale login sessions. Before pickup, automatically remove the suspended courier's
specific assignment through existing recovery. Restore the original reward and
reservation before reopening, preserve expiry, and fence pickup/new acceptance
while recovery is pending. A delayed or repeated instruction cannot remove a
replacement courier, act on a later assignment of the same courier, or repeat
financial changes.

Pickup and suspension follow whichever takes effect first. After pickup, retain
delivery completion/proof and cancellation with final location. Existing requester
orders and receipt confirmation continue. Normal B4 delivery timeout still applies.
No wait-for-delivery or deferred-suspension policy is introduced.

B5 has no standalone code scope. No general manual admin cancellation/removal or
admin UI is added. History/privacy stays with the existing history workflow,
financial recovery with B1, cancellation/disposition/pickup/receipt with B3, proof
with B2, and timers with B4.
B4's durable-receipt deadline policy must not be replaced with B2's processing-time
behavior. This change adds no timer/frontend business code or new microservice.

## Current-role authentication

Shared `httpauth.Authenticator` verifies JWT identity, then resolves the persisted
role through existing `GetMyProfile` on every protected shared-authenticator request.
User's internal listener exposes only that bearer-authenticated self-profile method,
using its local verifier and database authorization. Profile mutations remain on
the public listener. The same internal origin serves public keys and current roles.
Only the verified bearer is forwarded. Identity/session/timestamps remain from
the JWT; role and admin authority come from the fresh profile. No role cache or
fallback to stale privileges is permitted. Bad/missing account identity returns
401; unavailable, malformed, mismatched or redirected profile observations return
503. Lookup is bounded by context and five seconds. This adds a User Service
round trip and availability dependency, including for reads.

Suspended users remain authenticated for history, permitted participant actions
and receipt. User retains its local verifier/database authorization, avoiding
recursive profile lookup. B1's authenticated bearer forwarding remains intact.
A suspended initial pickup is rejected; recorded pickup repeats and suspended
requester receipt remain available.

## Durable User decision handoff

The existing `RoleService.ChangeUserRole` validates the admin, target and reason
under User row locks. Before reading/writing decision relationships, it locks the
target User. A suspension/reinstatement uses `role_dispatches` (migration 00014):
immutable decision ID, user/admin IDs, original/destination role, per-user revision,
reason and timestamps. No bearer credential is persisted.

1. Persist the immutable decision intent without finalizing User's role/audit.
2. Send `OrderInternalService.ApplyAccountSuspension` through an explicitly
   injected authenticated User-to-Order client; retain the same identity on retry.
3. Only after the matching Order acknowledgment, atomically finalize User's role,
   audited change and dispatch acknowledgment. Concurrent/restarted finalizers
   observe the completed row instead of writing a second audit.

A lost/uncertain reply retains durable intent and reports Unavailable. Changed
pending intent is rejected. Definite Order rejections are recorded with
`rejected_at`, reported as FailedPrecondition, and excluded from automatic recovery.
They retain the immutable decision and block replacement/finalization until explicit
reconciliation. Uncertain failures remain retryable. Migration 00015 guards rollback
past the dispatch history whenever any revision tombstone exists, including applied
or rejected decisions. It does not rewrite applied migration 00014.
Bounded recovery retries use the same decision, never a saved human session.
Reinstatement advances the revision; older delayed facts cannot undo it. This is
immediate command recovery, not a policy to suspend after delivery or at a scheduled
time. Successful admin completion requires the Order fence already committed. A crash after Order commit retains the intent needed to
finish the User projection. Immutable dispatch records preserve the originating
actor without blocking deletion; audit userstamps retain their existing nullable
semantics if that account disappears.

## Deleted targets and fail-closed startup

Soft-deleted target decisions remain as immutable history without a forged
acknowledgment or removal of the Order fence. They are excluded from recovery and
the startup guard. A recovery item loaded before deletion rechecks the target
before sending and finalizing.

A live target's unfinished or rejected decision still fails closed when transport
configuration is absent. User Service cannot start in that state, affecting login and the
fresh-role lookups used by Supplier and Order. This deliberately retains the
fail-closed boundary rather than silently abandoning live intent.

## Authoritative Order transaction

Order owns `order_account_suspensions`: last decision ID/revision, suspended flag,
originating admin and reason. Ordinary local mutations take a shared PostgreSQL
transaction advisory gate **before** idempotency/Order locks. Applying a decision
takes the same gate exclusively before any Order locks. Ordinary mutations remain
concurrent with one another. A rare suspension briefly blocks local mutations;
this favors a simple verifiable ordering boundary over per-user lock discovery
and stale-participant races. No remote call occurs while either gate is held.

Within that exclusive transaction, Order persists the versioned account fence,
locks affected parent Orders before fresh relationships, captures current
pre-pickup assignment identities into B3 recovery, and advances the generation of
pending acceptance work into B1 compensation. In-flight Adjust replies cannot
assign afterward. Pickup-first orders retain custody and continue delivery.
Reinstatement changes eligibility only; it never resurrects removed assignments.
Older decisions are rejected as stale without effects or a successful dispatch
acknowledgment. Conflicting payloads at the same revision fail closed. Rollback
cannot discard revision tombstones or active fences.

The sole Postgres Apply boundary checks the persisted account fence for creates,
offers, acceptance and pickup. It checks both the initiating user and the target
courier, covering a requester's acceptance of a suspended courier's old offer and
callers whose profile check happened before suspension. Recovery, delivery,
cancellation, disposition and receipt keep their existing participant rules.

## Reuse of assignment recovery

`Canceller.RemoveSuspendedAssignment` targets the immutable successful assignment,
not the current occupant of an Order ID. Duplicates join pending recovery without
another audit/event/Credit call. Missing assignment identity fails closed for
removal, not for the account fence: suspension still commits and prevents pickup,
while that assignment remains for reconciliation without a guessed reversal.
Picked-up or replacement assignments are unchanged. Automatic admission/completion
use explicit system audit actions. Discovery includes only assigned AwaitingPickup
orders and AwaitingCourier orders with relevant AdjustmentPending attempts or a
RevertPending attempt whose offer is still accepting. Existing RevertPending work
retains its generation, lease and compensation owner. Suspension voids the in-flight offer, so an
insufficient-credit compensation cannot reopen it even after reinstatement.

Migration `00012` adds the immutable suspension decision ID to B3's
cancellation record. Unpublished suspension migrations use Order versions 00012-00013 and User
versions 00014-00015, after the migrations already on main. Existing leases, generation fencing, retry backoff, startup recovery,
reservation checks and the central assignment-history writer are reused. Revert
uses the original immutable assignment ID outside the transaction; acknowledged
reversal plus a matching Held reservation at the original amount is required.
Only then does recovery re-lock/revalidate and restore reward/reservation, clear
the assignment and reopen with unchanged expiry. No second financial worker exists.
Database and RPC bindings are generated, not hand-edited.

## Istio workload authentication and User-only authorization

User dispatches to `ORDER_SERVICE_INTERNAL_URL` through the generated Order client.
The composition root starts durable recovery when configured. Missing configuration
still rejects suspension/reinstatement before new intent and refuses startup if a
live target has unfinished intent. Network reachability is not a startup dependency:
User must start before Order can fetch User's public keys. No human bearer token is
stored or forwarded, and redirects are refused.

The existing sidecars supply STRICT mTLS workload authentication. The
`order-suspension-user-only` Istio DENY policy rejects calls to
`/order.v1.OrderInternalService/ApplyAccountSuspension` on Order's internal port
8081 unless the authenticated principal is `cluster.local/ns/foc/sa/user-service`.
DENY takes precedence over the broad authenticated-namespace ALLOW. An arbitrary
FoC workload, expired admin JWT, or forged identity header cannot grant access.
No new workload credential system is introduced.

Order mounts only this method from OrderInternalService on its internal listener.
The public listener and Gateway do not expose it. The ordinary user-JWT middleware
is not the service-only authorization mechanism. Kubernetes deployments must keep
the sidecars, STRICT policy, and User-only DENY together with this application.
The Compose private network is a local-development seam without workload identity;
it is not production security evidence, and internal ports must not be published.

This supplies the suspension-specific wiring required by
[#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29). Live mesh authorization
and retry-after-expiry evidence remain a deployment verification gate. The code
and scripted RPC tests do not establish that a deployed mesh enforces the policy.
No cluster deployment or credential provisioning is performed by this change.

## Existing suspended accounts at rollout

The schema migrations create the new dispatch and Order-fence ledgers; they do not
invent or replay administrator decisions for accounts suspended before this
workflow existed. Fresh-role authentication applies to those accounts, but their
old assignments are not automatically removed by migration. Reconcile existing
suspensions and their original audit context before treating rollout as complete.
This change does not perform a production data migration or backfill.

## Verification and remaining gates

See
[verification evidence and repeat commands](../testing/immediate-suspension.md).

Passing local checks does not establish production authenticated suspension
completion. Real Credit, live mesh enforcement, and deployment remain uncertified.
