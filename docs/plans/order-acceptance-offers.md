# Order acceptance and offers

Status: user authorized non-blocked implementation. Draft PR #159 is based on
PR #150 at e056db71ca814eb96aa45aea96cb82e5aeefedd2. F0's acknowledged handoff
will become the integration base after publication. This document is a plan and
decision record, not proof of executable behavior.

## Scope and acceptance sources

Implement AcceptOrder, MakeOffer, AcceptOffer and assignment recovery. Reuse
ListOffers, the aggregate mutation boundary, transactional audit/outbox facts,
shared retry-key storage and the existing Credit RPC contracts. R1.2.1–4/6,
R1.6.2/12/13, C1.1.1–2, C1.2.2 and NFR-02/04/05/11 define the backend outcomes.
The original backlog is /Users/andrewsoon/dev/cs3219-proj/09-d1-product-backlog.md.

Do not implement Credit, reviews, aggregate ratings, moderation, Reports/Appeals,
or pickup/receipt backend from #148. R1.1.10–11 and R1.2.5 remain explicitly
incomplete until an authoritative rating-read dependency is supplied.

## Approved decisions

- D1: Each courier has at most one open offer per request. A new proposal voids
  that courier's prior open offer atomically. An offer may exceed available
  credits. Only accepting it attempts to increase the reservation.
- D2: Insufficient credits leaves the original hold unchanged and the public offer
  open. Other permanent historical offers do not reactivate on reopening.
- D3: One assignment attempt can be pending per request. Concurrent callers are
  told that assignment is in progress, not that a courier is already assigned.
  Failure does not automatically promote another courier.
- D4: An Accepting offer is immutable. Proposal replacement is rejected while
  assignment is pending. Parent-row locking serializes begin/replacement.
- D5: Assignment must commit before the applicable expires_at deadline. A late
  Credit success cannot authorize assignment. The original-expiry versus renewed
  reopening window remains B4's separate decision.
- D6: Order owns the assignment saga and recovery worker. Persist intent before
  network calls, perform no remote work under database locks, and persist
  compensation until Credit confirms it. Unknown results remain pending.
- D7: Every assignment, including direct acceptance at the original reward, calls
  AdjustReservation. Compensation calls RevertAdjustment. Opened Order terminal
  settlement uses order.outcome, never ReleaseReservation or SettleReservation.
- D8: AcceptOrder requires a client-generated attempt UUID. Retries reuse it,
  intentional reacceptance uses a new one, and identity/result retention lasts as
  long as the retained Order. Return historical attempt outcome plus current
  Order state. See ../adr/order-credit-pubsub.md.
- D9: Withdrawal/unassignment reopens at the original reward, after the required
  adjustment reversal. B3 must resolve its C1.2.11 refund interpretation before
  the exact reopening integration is finalized.
- D10: F0 owns shared read privacy/history/frontend composition. Former couriers
  retain redacted history, not later proof/exact destination/instructions. Offers
  alone do not prove participation. B1 writes successful assignment history only
  through F0's acknowledged transactional seam.

## Implementation and coordination

New feature files implement a consumer-owned AssignmentRepository/AssignmentTx,
Assigner, immutable assignment intent and leased recovery. A transaction-bound
repository adapter invokes Mutator.Mutate so workflow writes and aggregate
Transition/NewChange/Apply changes commit together. A selected counteroffer needs
its own requester-authorized assignment action so audit does not impersonate the
courier. Offer metadata actions need explicit same-status policy authorization.

Each financial attempt needs an immutable identity. Proposed mapping: public
Offer.ID identifies the courier's proposal, AssignmentAttempt.ID is Credit's
per-attempt offer_id. This prevents a permanently refused/reverted financial key
from preventing later attempts on an insufficient-funds proposal. This mapping
requires contract review and acknowledgment, not an assumption about Credit's
backend. Direct acceptance also records an offer for the assigned amount.

Shared edits require explicit owner acknowledgment before changes: Order proto
and generated Go/TypeScript, aggregate action/field/fact policy, RPC handler,
composition/worker lifecycle, schema numbering, and assignment-history write seam.
Messenger currently refuses further DMs at its ten-message session limit. The
coordination need is visible in OrderOffers status. No silent dependency merges,
guessed UI exports, or guessed history schema are permitted.

The future withdrawal seam must fence any in-flight attempt, persist required
compensation, and avoid reopening for a new assignment before reversal is
confirmed. An earlier successful attempt remains historical after withdrawal.
The exact B1/B3/B5 API and F0 history schema are not published yet.

## Verification plan

New test expectations must cite the backlog, approved decisions above, or the
Credit RPC contract. Targeted client tests use a generated Connect client and
scripted server, proving wire values, workload identity and current-state
validation, not live financial execution.

At feature-ready, run a repeatable medium-to-hard PostgreSQL/Connect scenario:
50 competing direct accepts; lost Credit response and restart; expiry/cancellation
while adjustment is in flight; persistent compensation and replay; repeated
successful acceptance after withdrawal cannot reassign; offer replacement races;
and two 20-credit reservations competing to rise to 50 against a 70-credit balance.
The latter is a scripted Credit contract test until the real owner implements it.
Coordinate shared E2E infrastructure with OrderOrchestrator before Docker runs.

Run repository formatting/lint, isolated Go checks, Buf lint/generation/breaking
checks and sqlc generation for changed contracts/schema. Existing standalone
checks run while developing. At most two review/verify/triage/fix rounds. Exact
independent devin/claude-opus-5.5 design/implementation reviews are required, or an
explicit user waiver. Never claim fixture-based financial integration as live
Credit fulfillment.

After all non-blocked work is done, start a bounded managed waiter that checks F0
readiness every five minutes. Rebase only onto the exact acknowledged exporter
commit, finish dependent integration, and publish consumer/exporter identities.
