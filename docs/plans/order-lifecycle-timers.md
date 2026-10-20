# Order lifecycle timers

## Scope and status

B4 covers expiry and delivery timeout (R1.6.11/R1.6.10) and serialization
against acceptance (R1.2.4). The current implementation is the isolated expiry
slice only, on foundation/creation base `e056db71ca814eb96aa45aea96cb82e5aeefedd2`.
It is not mounted in the service composition root. No dependency branch has been
merged. This is not a completed lifecycle-timer feature.

The goal is one feature PR. Review of creation, counteroffer and assignment
submission-versus-completion deadlines is outside this implementation and tracked
in [issue #178](https://github.com/AY2627S1-CS3219-P1/FoC/issues/178).

## Agreed behavior

- Q1: A valid delivery confirmation durably received by Order before
  `picked_up_at + 2 * delivery_duration` remains eligible after processing delay.
  The server, not the phone, supplies receipt time. An identical retry of a
  previously accepted confirmation remains successful.
- Q2: Withdrawal or admin unassignment does not extend the original expiry.
  Financial reversal must finish before another assignment is possible. That
  recovery belongs to the assignment/cancellation owners, not the expiry worker.
- Q3: Closure and audit use actual processing time, not the missed deadline.
- Q4: Reuse the shared countdown and server-authoritative active-page refresh.
  Never synthesize terminal status or completed refunds from a browser clock.
- Q5: Order owns persisted deadline discovery, immediate startup scanning and
  normal one-second polling, in bounded batches with locked rechecks.
- Q6: Retry transient failures with bounded backoff; process unrelated orders.
  Kafka/Credit outages do not prevent local lifecycle commits.
- Q7: Proof must finish uploading before confirmation submission. A recorded
  submission is not extra upload time. The approved upload-outage exception is
  separate and still needs authoritative evidence.
- Q8: Unavailable proof inspection leaves an on-time submission pending with
  retries and alerts for prolonged failure. Invalid proof does not prevent an
  overdue timeout. Authorization and cancellation rules still apply.
- Q9: If confirmation is valid, its server-receipt time determines effective
  delivery for lateness/reward. Processing/audit time remains separate. Credit
  owns the reward calculation and settlement.

## Expiry implementation

`ExpiryTimers` uses the existing `Mutator` to lock the order, read fresh offers,
recheck the current status/time, call `Transition`, and build `NewChange`.
Only due Awaiting Courier orders become Expired. The order, offer changes,
System audit and terminal outcome commit in the same existing transaction.
Expiry preserves the original deadline, reservation and other observations.
Open/Accepting offers become Void; historical Accepted/Void offers remain intact.
B1 recovery must still compensate a late Credit response; changing an offer's
status is not financial settlement.

Discovery uses a separate sqlc query with UUID keyset pagination and batches of
50. Four workers use five-second per-order attempt contexts. The page cursor
advances past transaction failures so a broken early candidate cannot starve the
next page, then wraps to retry remaining overdue orders. The cursor is advisory,
not a claim or an acknowledgement. A new worker starts from persisted due rows.
Discovery and per-order attempts each have five-second contexts. Individual
failures use separate advisory retry delays, capped at thirty seconds, while
healthy-order discovery continues on its normal one-second cadence. Retry
bookkeeping for orders no longer discovered is removed after a full sweep.
Only discovery failures back off the whole run loop. Logs expose failed scans
and per-order attempts. No new migration, external scheduler, financial RPC or
network call is added.

## Required integration handoffs

1. B2 PR #177 at `e867613d41f0860d056331c6820fe4d452c53dc5` still applies a
   processing-time delivery deadline. It supplies neither durable pending
   submissions nor a receipt/proof-preexistence fence. It also lacks concrete
   proof persistence, RPC and composition adapters. Do not infer those exports.
2. B2/B4 must reconcile receipt admission, proof validation/recovery and timeout
   under the same parent lock. Pending submissions cannot be ignored by timeout,
   and an invalid submission cannot suppress failure indefinitely. Delivery time
   in outcomes must follow Q9 without backdating audit records. No timeout worker
   or speculative proof schema is added before that concrete handoff.
3. B1 PR #159 at `de1968e0f20174bf1f20fcb89986b0fbfd66d5ee` and its UI dependency
   are not on this base. Local tests do not claim its acceptance RPC or actual
   Credit compensation works together with this slice. Its explicit final
   assignment-before-expiry contract remains unchanged.
4. Startup wiring requires an acknowledged composition-root change. No second
   worker/controller or dependency merge is introduced silently.
5. Timeout warnings need a reason distinct from cancellation warnings and a
   coordinated producer/consumer contract. B3 retains final-location and
   cancellation ownership. No warning schema or encoder is changed here.
6. OrderUI owns shared routes/services and refresh integration. Existing shared
   behavior is on dependency branches, not this base. No duplicate UI is added.

## Verification

Test expectations come from R1.2.4/R1.6.11 and the agreed behavior above, not from
reading implementation output. Run from the repository root:

```sh
make fmt-check
make lint
(cd order-service && GOWORK=off go test -mod=readonly -race ./internal/order -count=1)
(cd order-service && GOWORK=off go test -mod=readonly ./...)
(cd order-service && env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order -run '^TestPostgresExpiryTimers$' -count=1 -v -timeout=10m)
```

The PostgreSQL command uses a disposable PostgreSQL 18 testcontainer with random
ports. Do not set `TEST_DATABASE_URL` to a shared database: the existing fixture
resets its schema. The test covers 50 competing expiry mutations, exact durable
audit/outcome counts and preserved financial/deadline observations; 52 overdue
orders with an injected transaction failure, next-page progress and restart
recovery; and an expiry mutation waiting on a real assignment row lock before
rereading state. It does not exercise a browser, B1's Credit saga, B2 delivery
submission, Kafka consumption or actual payout/refund.

First verification pass: root formatting/lint, race-enabled domain tests and all
Order Service fast tests passed. PostgreSQL passed the 50-contender and 52-order
failure/restart cases. The lock-wait observer incorrectly assumed that sqlc kept
`SELECT *`; it was corrected to match the actual lock query and blocking PIDs.
The first independent read-only review could not verify its requested model,
so it does not count toward the required exact-model review gate. Its useful
finding was corrected: one candidate's failure no longer delays healthy scans.
Discovery attempts are now bounded too. Added checks cover healthy-order progress
during backoff, transient run-loop recovery, shutdown during blocked work,
withdrawal/unassignment expiry, cancellation racing expiry and real offer writes.
Second/final verification passed root formatting/lint with zero issues, all
Order Service tests with the race detector, and all five PostgreSQL scenarios:
50 expiry contenders, 52-order failure/restart progress, withdrawal/unassignment
with original expiry, cancellation contention, and the real assignment lock wait.
No application edits followed that pass. Independent native Opus 5.5 review
found no confirmed bugs and accepted retaining this isolated slice as a declared
partial implementation. Actual `generation_model = claude-opus-5-5-medium` was
verified in its native export, together with read/grep/find-only tools and
unchanged hashes for all six application/query/test files. This is not full
feature or integration approval. Private native exports are not shared artifacts.

Remaining review warnings: discovery uses instance clocks and assumes reasonable
clock synchronization; its current query has no purpose-built index or scale
benchmark; UUID paging bounds work per page but can delay discovery through a
large backlog. Discovery-failure backoff/timeout lack dedicated execution tests,
and the blocked-attempt fake has a small duplicate-channel-close flake risk if
cancellation is delayed beyond the attempt timeout. No fixes or tests were added
after the final pass. Full-feature integration remains blocked by the handoffs
above.
