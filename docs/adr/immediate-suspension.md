# ADR: Immediate suspension, durable handoff and Order authority

Date: 20 Oct 2026
Status: local behavior implemented/tested; production disabled pending #29;
required Devin Opus review and PR publication not completed.
Base: B3 PR #179, pinned `dea9c7c78130853793e8e5a8774f607cace02256`.
Branch/worktree: `andrew/feat/immediate-suspension-b3`, `/workspace/FoC-suspension`.
Original auth work `f1b8957` remains preserved; cherry-pick `abb3cb1` applies it
without overwriting B3. `99c5a49` preserves assignment-specific removal/recovery.

## Approved behavior and ownership

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
admin UI is added. History/privacy stays with F0, financial recovery with B1,
cancellation/disposition/pickup/receipt with B3, proof with B2, and timers with B4.
B4's durable-receipt deadline policy must not be replaced with B2's processing-time
behavior. This change adds no timer/frontend business code or new microservice.

## Current-role authentication

Shared `httpauth.Authenticator` verifies JWT identity, then resolves the persisted
role through existing `GetMyProfile` on every protected Supplier/Order request.
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
target User. A suspension/reinstatement uses `role_dispatches` (migration 00007):
immutable decision ID, user/admin IDs, original/destination role, per-user revision,
reason and timestamps. No bearer credential is persisted.

1. Persist the immutable decision intent without finalizing User's role/audit.
2. Send `OrderInternalService.ApplyAccountSuspension` through an explicitly
   injected authenticated User-to-Order client; retain the same identity on retry.
3. Only after the matching Order acknowledgment, atomically finalize User's role,
   audited change and dispatch acknowledgment. Concurrent/restarted finalizers
   observe the completed row instead of writing a second audit.

A lost/uncertain reply retains durable intent and reports Unavailable. Changed
pending intent is rejected. Bounded recovery retries use the same decision, never
a saved human session. Reinstatement advances the revision; older delayed facts
cannot undo it. This is immediate command recovery, not a policy to suspend after
delivery or at a scheduled time. Successful admin completion requires the Order
fence already committed. A crash after Order commit retains the intent needed to
finish the User projection. Immutable dispatch records preserve the originating
actor without blocking deletion; audit userstamps retain their existing nullable
semantics if that account disappears.

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
Older decisions are acknowledged without effects; conflicting payloads at the same
revision fail closed. Rollback cannot discard revision tombstones or active fences.

The sole Postgres Apply boundary checks the persisted account fence for creates,
offers, acceptance and pickup. It checks both the initiating user and the target
courier, covering a requester's acceptance of a suspended courier's old offer and
callers whose profile check happened before suspension. Recovery, delivery,
cancellation, disposition and receipt keep their existing participant rules.

## Reuse of assignment recovery

`Canceller.RemoveSuspendedAssignment` targets the immutable successful assignment,
not the current occupant of an Order ID. Duplicates join pending recovery without
another audit/event/Credit call. Missing assignment identity fails closed; picked-up
or replacement assignments are unchanged. Automatic admission/completion use
explicit system audit actions.

Migration `20261020140000` adds the immutable suspension decision ID to B3's
cancellation record. Timestamp versions avoid claiming B4's next Order migration
number. Existing leases, generation fencing, retry backoff, startup recovery,
reservation checks and the central assignment-history writer are reused. Revert
uses the original immutable assignment ID outside the transaction; acknowledged
reversal plus a matching Held reservation at the original amount is required.
Only then does recovery re-lock/revalidate and restore reward/reservation, clear
the assignment and reopen with unchanged expiry. No second financial worker exists.
The cancellation-migration test now explicitly rolls back migration 6 instead of
assuming it is the newest migration. All sqlc/Buf output is generated.

## Deferred service-auth dependency and production behavior

The authenticated retry/trusted-caller dependency is tracked in existing
[#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29). No duplicate issue or
separate auth implementation is added after Andrew deferred that work.

PR #167 at `f51a16a85dd073bfc70c5b49d514e78d1017b2fb` was fetched/read as context:
separate User/Order service accounts, mandatory proxies, STRICT Istio mTLS,
loopback listeners and internal Order port 8081 denied to the gateway. Its current
policy trusts all FoC workloads, so it does **not** establish User-only authority
for this mutation. It is open/unmerged and live deployment is unverified.

The clean prerequisite branch `andrew/feat/user-order-suspension-auth` preserves
that inspected source at `/workspace/FoC-user-order-auth`; it has no tracked
implementation/configuration changes. Suspension does not duplicate #167.

Production deliberately leaves `RoleService.Effects` nil: suspension/reinstatement
fails with FailedPrecondition BEFORE admitting a role/dispatch change. Startup
also refuses to silently abandon an existing unfinished dispatch if transport is
unavailable. `SetupSuspension` requires BOTH authentication middleware and a
User-only authorization interceptor, otherwise returns 503. It is not mounted in
the public router or current production composition. Test identities exist only
in tests. No plaintext fallback, trusted client header, Credit-token reuse,
credential creation, persistent access or live policy change is introduced.

To enable production, #29 must supply the authenticated retry client, intended-
caller authorization, internal-listener composition and approved deployment
configuration. Then inject the client/guards, start the existing dispatch recovery
loop, and mount the private handler through #167's listener. Supplying an arbitrary
HTTP client or trusting the current broad namespace policy is insufficient.
These are explicit unmet deployment/transport dependencies, not passing live
security evidence.

## Review and remaining verification

Andrew requires Devin CLI with Opus, or an additional Devin Opus review on the
public PR. No `pi`, `devin`, `devin-cli` executable or callable Devin connector is
available here; no substitute model was launched. PR metadata lookup through
`gh pr view 179` returned `Forbidden` from GitHub GraphQL and was not retried via
another API. No exact-model review or ready-PR status is claimed.

See [verification evidence](../testing/immediate-suspension.md). B2 proof and B4
live timer behavior, real Credit, mesh enforcement and deployment remain uncertified.
