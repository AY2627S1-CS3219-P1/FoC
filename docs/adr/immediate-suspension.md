# ADR: Immediate suspension and integration boundaries

Date: 20 Oct 2026
Status: authorization groundwork implemented; Order integration blocked
Base: main `f0afe820041c339b89e3107b1a090319dcf8aa37`

## Approved behavior

An explicit administrator decision with a nonblank reason suspends immediately.
Warnings never suspend automatically. Block new requests, acceptance and pickup,
including requests made with a token issued before suspension. Do not defer
suspension until delivery ends.

Before pickup, remove the suspended courier through durable recovery: restore
the original reward reservation before reopening, retain the original expiry,
and fence pickup and new acceptance throughout recovery. After pickup, retain
delivery completion/proof and cancellation with final location. Normal delivery
timeout still applies. Existing requester orders and receipt confirmation remain
available. Pickup and suspension must serialize so whichever takes effect first
determines the outcome.

Automatic removal targets the suspended courier's specific assignment. A delayed
or repeated instruction must neither remove a replacement courier nor repeat
credit changes. Reuse existing assignment identity and credit-recovery work;
an order ID or courier ID alone is not an assignment identity.

## Implemented independent boundary

Reuse User Service's audited `ChangeUserRole` operation and its existing
`ProfileService.GetMyProfile` RPC. Shared `httpauth.Authenticator.Authenticate`
first verifies JWT identity, then resolves the current persisted role on every
protected request. Forward only the verified bearer token to the configured User
Service, never cookies. Keep identity/session/timestamps from the verified token;
derive both role and admin authority from the fresh profile. No role cache or
fallback to token privileges is permitted. Unknown roles, missing/mismatched
profiles, lookup errors and redirects cannot reach the protected handler.
Unauthenticated/missing accounts return 401; unavailable or invalid role
observations return 503. Lookup is bounded by the request context and five seconds.

Suspension continues to authenticate. Domain methods retain participant access
and decide which mutations are allowed; a blanket suspension denial would break
completion, receipt and history. User Service keeps its local verifier and fresh
database authorization, so this lookup does not recursively call itself.

Supplier and Order already mount this authenticator. This adds one User Service
round trip per protected request and makes its availability a request-time
dependency even when the public key cache is warm. No new service, credentials,
permissions, protobuf contracts, schema, timer, frontend or service refactor is
introduced. Existing refresh/login behavior is retained.

This closes stale-role authorization for requests whose role lookup observes the
committed suspension. It does **not** linearize a subsequent Order mutation with
a concurrent suspension, revoke sessions, automatically unassign couriers, or
cancel already admitted assignment work. Do not present this groundwork as the
complete immediate-suspension feature.

## Verified dependencies and stopped work

Read-only fetch on 20 Oct confirmed these exact reachable PR heads; none is an
integrated implementation base in this checkout:

| Dependency | Head | Relevant boundary |
| -- | -- | -- |
| [#159](https://github.com/AY2627S1-CS3219-P1/FoC/pull/159) | `de1968e0f20174bf1f20fcb89986b0fbfd66d5ee` | Creation/assignment recovery, original reservation and stable attempt identity; F0 remains external. |
| [#171](https://github.com/AY2627S1-CS3219-P1/FoC/pull/171) | `59dd1b9bb9124b84797d1225e2fee8f1f9065bf2` | Durable withdrawal/reopening with pending fence, reversal acknowledgement and current Held reservation verification. |
| [#148](https://github.com/AY2627S1-CS3219-P1/FoC/pull/148) | `593e6521ba2208f43c4cdfd67bc21da89a521079` | Pickup/receipt progress; separate from withdrawal head. |
| [#177](https://github.com/AY2627S1-CS3219-P1/FoC/pull/177) | `e867613d41f0860d056331c6820fe4d452c53dc5` | Partial proof work with unresolved findings; not an approved completed base. |

The supplied B3 cancellation/pickup/receipt/frontend and B4 expiry work is local
and unavailable here. Main has read RPCs and aggregate foundations, with mutation
RPCs still unimplemented. Do not reconstruct those features or cherry-pick the
heads into a speculative integration branch.

Once an integrated base exists, implement the suspension-specific coordination
inside the existing User/Order ownership boundaries. The Order parent row must
be locked before fresh relationship reads. Pickup and suspension need a common
durable ordering boundary; a preflight role lookup alone is insufficient. Reuse
the withdrawal recovery contract, including the successful assignment attempt's
immutable Credit identity, generation fencing and reservation verification.
Persist recovery intent before remote work, call Credit outside the Order lock,
and re-lock/revalidate before reopening. Check pending acceptance too, including
a requester's acceptance of an offer made by a suspended courier. A request-time
lookup only checks the caller and cannot perform that check for another account.
Retain the targeted assignment identity throughout recovery and compare it again
under the parent lock before applying removal. Repeated instructions must join or
recognize the original operation and reuse its financial identities. Required
integration evidence includes suspend A, replace with B, then deliver delayed and
duplicate A-removal instructions: B remains assigned and no additional credit
change occurs. A later assignment of A must also be distinct from A's old one.

Keep B3's pickup/receipt adapters and final-location cancellation ownership, B2's
proof implementation, and B4's timers. B4's approved durable-receipt deadline
policy differs from B2's processing-time behavior; no deadline wiring belongs in
this change. Preserve expiry through reopening and test restart/retry and both
pickup/suspension orderings when the shared boundary is available.

B5 has no standalone code scope. General manual administrator cancellation or
removal actions and supporting administrator UI are excluded. Automatic courier
removal stays in suspension, history/privacy stays with F0, financial recovery
with B1, and cancellation/withdrawal with B3. This patch adds no general admin UI.

#159 adds `auth.WithUserBearer` immediately after the caller context is built in
the same authenticator. Preserve that forwarding when integrating this patch;
the current base does not contain that helper. No generated file is edited here.

GitHub issue searches for B4/B5 discussion were denied (`Forbidden` from
`https://api.github.com/graphql`). Those reads were not retried. The supplied
approved requirements and fetched branch documents are the available evidence;
the missing discussions have not been represented as reviewed.

See [verification evidence](../testing/immediate-suspension.md).
