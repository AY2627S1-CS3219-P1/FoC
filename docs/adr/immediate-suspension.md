# ADR: Immediate suspension and integration boundaries

Date: 20 Oct 2026
Status: partial implementation; dispatch, authoritative ordering and exact-model review blocked
Dependency: B3 PR #179, pinned `dea9c7c78130853793e8e5a8774f607cace02256`.
Branch: `andrew/feat/immediate-suspension-b3` in `/workspace/FoC-suspension`.
The original auth groundwork `f1b8957` and branch remain preserved; cherry-pick
`abb3cb1` applies it to the pinned B3 source without overwriting B3 changes.

## Approved behavior

An explicit administrator decision with a nonblank reason suspends immediately.
Warnings never automatically suspend. Block new requests, acceptance and pickup,
including requests made with a token issued before suspension. Do not defer
suspension until delivery ends.

Before pickup, automatically remove the suspended courier through durable
recovery: restore the original reservation and reward before reopening, retain
original expiry, and fence pickup/new acceptance during recovery. Target the
courier's specific assignment. Delayed or repeated instructions must neither
remove a replacement courier nor repeat financial changes. A later assignment
of the same courier is also distinct from the original target.

After pickup, preserve delivery completion/proof and cancellation with final
location. Existing requester orders and receipt confirmation continue. Normal
B4 delivery timeout remains applicable. Whichever of pickup and suspension takes
effect first determines custody handling.

## Implemented boundaries

Shared `httpauth.Authenticator` verifies JWT identity and resolves the current
persisted role through the existing `ProfileService.GetMyProfile` RPC on every
protected request. Only the verified bearer token is forwarded; cookies are not.
Identity/session/timestamps remain from the JWT, while role and admin authority
come from the profile. No role cache or fallback to token privileges is allowed.
Bad/missing account identity returns 401; unavailable, malformed, mismatched or
redirected profile observations fail closed with 503. Lookup is bounded by the
request context and five seconds. This adds a User Service round trip and
availability dependency for protected Supplier/Order requests, including reads.

Suspended users remain authenticated for history, participant actions and receipt.
User Service retains its local verifier and database authorization, avoiding a
recursive profile lookup. Existing authenticated bearer forwarding from B1 is
preserved. An initial pickup with a suspended caller is rejected in `Progress`;
a recorded pickup repeat and suspended requester receipt remain available.

`Canceller.RemoveSuspendedAssignment` is an internal admission method for an
**authenticated suspension dispatcher that still needs to be integrated**. It is
not a mounted endpoint and is not a general manual admin-removal API. Its input
identifies the persisted admin decision, Order, courier and immutable successful
assignment. It locks the Order parent before fresh relationships and assignment
reads. Wrong/later assignments and already-picked-up orders are unchanged;
missing assignment identity fails closed. Duplicate instructions join existing
recovery without another audit, event or Credit call.

Automatic removal uses B3's existing cancellation table, pending pickup fence,
startup scan, leases, generations, retry backoff, reservation verification and
central history writer. Migration 20261020140000 adds the immutable suspension decision ID
and assignment constraint/index; sqlc 1.30.0 regenerated the bindings.
Its timestamp version avoids claiming B4's next sequential migration number. No second
recovery worker or financial implementation is introduced. Automatic admission
and completion use explicit system actions, preserving accurate audit attribution.

B3 recovery calls Revert outside the Order transaction with the original immutable
assignment identity, requires acknowledgement, and reads a matching Held reservation
at the original amount. Only then does it re-lock and verify pending identity,
generation, custody, courier and assignment time before clearing the assignment
and restoring reward/reservation. Expiry remains unchanged. A downgrade refuses
to discard a pending suspension's identity. Existing participant cancellations
retain their behavior. The cancellation migration regression now explicitly
rolls back through migration 6 instead of assuming it is the newest migration.

## Remaining product integration

The pinned source supplies B3 cancellation, withdrawal, pickup/receipt adapters,
disposition and frontend. The original missing-B3-source blocker is resolved.
The remaining gap is the **User admin decision-to-Order dispatch and common
ordering boundary**. User role changes commit in User Service's database; Order
pickup commits under a different database lock. No role-change dispatcher,
suspension event consumer or authenticated coordination contract connecting those
operations exists in this source. `GetMyProfile` only resolves the caller, and
its result can become stale before an Order mutation commits.

The internal removal command serializes against pickup once admitted to Order;
it does not itself make User Service's earlier role commit atomic with that
admission. The current role lookup and pickup guard cannot establish the required
end-to-end first-effect ordering, cannot automatically enumerate/remove existing
assignments, and cannot fence already-admitted assignment work or a requester's
acceptance of another courier's old offer. Do not present this partial work as
complete immediate suspension or mount the command without authenticating its
persisted admin decision and resolving durable dispatch/ordering failures.

A request for the existing approved integration/source was raised while independent
work continued. No ad hoc cross-service database join, remote call under an Order
transaction, bearer credential persistence, new credential/permission or generic
suspension service was introduced to hide this gap.

## Ownership and completion gates

B5 has no standalone code scope. General manual administrator cancellation/removal
and supporting admin UI are excluded. Automatic removal stays in suspension,
history/privacy with F0, financial recovery with B1, and cancellation/withdrawal
with B3. No frontend or timer code is changed here. B2 proof and B4 deadlines
remain their owners' work; B4's approved durable-receipt policy must not be replaced
with B2's processing-time behavior. B2 proof is not integrated into PR #179, so
live post-pickup proof behavior is not certified here.

Andrew's required review is **Devin CLI with Opus**, with an authorized fallback
to an additional Devin Opus review pointing at the public PR. No `pi`, `devin` or
`devin-cli` executable or callable Devin connector is available in this environment.
The reviewer-model landmine requires actual provider/model provenance; another
model must not be substituted. No review was launched and no PR was marked ready.
No new credentials, spending, push, publication, merge or deployment occurred.

See [verification evidence](../testing/immediate-suspension.md) for passed checks,
unrun integration cases and exact denied download targets.
