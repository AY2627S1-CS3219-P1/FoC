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

D11: Orchestrator acknowledged ownership in PR #159 comment 6249016717.
B1 owns migration 00005, separate assignment adapter/query files, acceptance/offer
action policies, feature RPC methods and worker wiring. F0 owns migration 00004,
the sole atomic history hook in existing Transaction.Apply and response/read
projection. B1 delegates to existing Apply without extra history SQL or API calls.
Messenger sends now work, clearing the earlier ten-message coordination blocker.

D12: Reserved protobuf additions do not collide. F0 uses Order.delivery_building_name
field 23 and ListOpenOrdersRequest.delivery_building_name field 5, retaining but
rejecting exact delivery_location_id field 2. B1 reserves AcceptOrderRequest.attempt_id
field 2, AcceptOrderResponse.attempt field 2 and AcceptOfferResponse.attempt field 3.
The new AssignmentAttemptResult has attempt_id/status/rejection fields 1/2/3 and a
separate enum. Orchestrator explicitly requires waiting for the verified F0 backend
handoff before integrating partial peer commits or finalizing B1 generation.
F0's proto-only 2e663e1 and frontend da8e5fc are not that backend handoff.
F0 subsequently published verified backend head 6066038 atop 2e663e1, based on
e056db71ca814eb96aa45aea96cb82e5aeefedd2. Orchestrator accepted that dependency
handoff and requires a verified clean B1 checkpoint before the authorized rebase.
Published history must not be force-updated without explicit authorization.

D13: F0's acknowledged response seam remains shared.OrderView(ctx, caller, order,
images), a projection rather than visibility authorization. B1 must first obtain
current/historical visibility through Service.GetOrder using its same-snapshot
history policy. A failed/offer-only attempt without read visibility retains its
own attempt/offer response and omits Order on ErrNotFound. No raw shared.Order
projection of a historical attempt's current aggregate is allowed.

D14: A definitive insufficient-credit response still enters RevertPending until
Credit acknowledges a fence and a fresh matching baseline is observed. This avoids
assuming insufficient-funds errors are permanently cached while an older generation's
same-key Adjust may still be delayed. The public counteroffer reopens only after
that fence; direct synthetic offers become Void. The written external contract is
proto/credit/v1/reservation.proto: mutation keys never expire, and an unknown offer
is recorded as reverted so later same-key Adjust fails ADJUSTMENT_REVERTED. Scripted
fixtures prove Order follows this contract, not that real Credit implements it.

D15: F0 and orchestrator acknowledge the outcome mapping: rejected acceptance
returns failed_precondition with typed AssignmentAttemptResult details. Pending or
compensating results do not mean successful assignment. Historical success returns
its retained attempt plus caller-authorized current Order. B1's feature-local
client must decode error details and invoke shared refresh after mutations.

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

## Integration checkpoints and remaining verification

The five-minute bounded handoff monitor ran and was stopped once F0 published
verified backend 6066038. The verified B1 pre-rebase checkpoint 17f535a was pushed
to draft PR #159. The clean working branch was then rebased onto exact 6066038,
excluding later F0 commits. The only conflict was generated models.go, resolved by
sqlc 1.30.0 regeneration from combined migrations 00004 and 00005. Remote history
remains untouched after 17f535a because force-push is not authorized.

Both formal pre-rebase PostgreSQL verification rounds passed. The second covers
50 competing accepts, retained successful/rejected replay after removal, the
70-credit/two-20-hold counteroffer scenario, blocked refusal fencing and restart,
late adjustment expiry compensation, frozen proposal replacement/cancellation,
and the existing aggregate foundation tests under the race detector. These
are application + real PostgreSQL18 tests with scripted Credit responses, not
real Credit cross-service E2E. Evidence: ignored
.local/order-offers-evidence/postgres-round-{one,two}.jsonl.

The two formal rounds are consumed for this feature. Rebase and new API/UI work
do not reset that cap. New combined-lineage DB/browser verification is NOT run or
claimed. Orchestrator requires explicit user authorization before another formal
round. Ordinary targeted development checks remain allowed.

New contract generation/lint/breaking checks, isolated mounted Connect unit tests,
generated Go compilation, Svelte type checks and in-memory feature-client tests
cover the API/UI slice only. The required UUID intentionally rejects old keyless
callers despite protobuf wire compatibility. The feature component is
$lib/components/order-offers/OrderOfferControls.svelte with acknowledged props
{order, refresh, api, callerId, suspended}; factory createOrderOffersApi(transport)
is in $lib/services/order-offers. F0 retains singleton/detail snippet composition.
No shared frontend file is modified by B1 and no visible integrated UI/browser
result is claimed. F0 must consume an exact published exporter handoff first.

Proposed additional verification after explicit authorization: run combined
migration/history/assignment tests under PostgreSQL18 and the race detector,
exercise the acceptance API plus retained failed/successful replay through a
mounted Connect server backed by that database, then compose the feature control
through F0 and run a browser scenario for lost responses, refresh-retained keys,
pending/compensating/rejected feedback and caller change. Existing repeatable
backend command (not rerun after rebase):

```sh
cd order-service
GOWORK=off go test -mod=readonly -race -tags=integration \
  ./internal/order ./internal/rpc/order ./internal/rpc/shared \
  -count=1 -timeout=10m
```

The combined financial service remains a scripted contract seam until Credit's
real implementation and financial-key mapping are verified.

## Independent B1 review and final dispositions

Native Devin session bygone-haircut, runtime 3000.11.92, completed the independent
review of 6066038..cf4b42c and the scoped follow-up cf4b42c..8e81430. Both exports
identify the actual agent as Claude Opus 5.5 Medium and every inference model as
claude-opus-5-5-medium. The follow-up export contains two completed final messages
and only read/grep/find tools, with no execution, edits or tests. Raw exports,
completed findings and sanitized provenance remain ignored under
.local/order-offers-exact-review/. This is B1 evidence, not borrowed F0 approval.

- **F1:** Definitive admission/input rejection no longer locks the UI into endless
  retries. Financial-processing and current-view failures after admission remain
  unavailable, not definitive rejection. The follow-up confirmed these paths but
  found that suspension can deny a replay after the original was admitted.
  Final fix 9f09e2a leaves PermissionDenied uncertain and extends the client
  regression cases. That final fix and test edit are UNVERIFIED: no further
  verification or independent-review round was run after the second review.
- **F2:** Repeating counteroffer acceptance explicitly warns that recovery may
  allow a new financial attempt and requires confirmation. It is not described
  as a read-only check. Confirmed by the scoped follow-up, not browser execution.
- **F3:** Aborted competition now has explicit busy feedback and preserves the
  saved identity. Direct and counteroffer transport outcomes remain intentionally
  different because only direct acceptance has a caller-generated attempt UUID.
  Confirmed by the follow-up.
- **F4:** OPEN, https://github.com/AY2627S1-CS3219-P1/FoC/issues/161.
  Fresh acceptance UUIDs against an existing unavailable Order can accumulate
  permanent rejected attempts and reveal limited private-ID existence. The issue
  records the exact source-traced sequence and conflict between admission abuse
  limits, historical identity retention and nonparticipant visibility. No live
  abuse reproduction or policy acceptance is claimed.
- **F5:** OPEN, https://github.com/AY2627S1-CS3219-P1/FoC/issues/162.
  After the documented 24-hour MakeOffer key guarantee expires, an old saved
  proposal retry can void even a newer Open offer from the same courier. Valid
  request expiry has no 24-hour maximum. This is permitted outside the server's
  guarantee, but the client's unbounded retry horizon is unresolved. The issue
  records a source-traced sequence, not a delayed integration reproduction.
  Expired retries of already accepted/void proposals may also be presented as
  rejected although originally recorded. No retention-policy change is approved.
- **F6:** Caller/order changes invalidate pending offer reads; late errors check
  the captured scope as well as the read counter. Confirmed by the follow-up.

At 8e81430, targeted Go domain/Connect units, 11 in-memory client/storage tests,
Svelte checks (zero errors/warnings), production build and formatting/lint passed.
These results do not cover final fix 9f09e2a, full PostgreSQL/Connect assignment
integration, real Credit, or mounted browser controls. F4/F5 remain unresolved,
not waived. Exact review execution is complete with findings; feature approval
is not claimed. Remote PR #159 remains 17f535a. Publishing the rebased local branch,
F0 shared UI composition and any new formal verification require their existing
explicit authorizations/handoffs.
