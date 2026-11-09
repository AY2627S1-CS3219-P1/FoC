# Order lifecycle timers

## Scope and status

B4 covers expiry and delivery timeout (R1.6.11/R1.6.10) and serialization
against acceptance (R1.2.4). The isolated expiry slice was verified first on
foundation/creation base `e056db71ca814eb96aa45aea96cb82e5aeefedd2`. Its unpublished
commits were then rebased onto the explicitly acknowledged B2 exporter
`81e88b3248553600ba33ea186d5d4fa2802a70c1`, then final B2 backend/root exporter
`3168bec01e2ff81f866103a0a9688814a0afc165`, which includes B3's delivered
cancellation/progress contracts. Timeout consumes its concrete proof transaction,
pending predicate and database clock. Both workers are mounted beside proof
recovery through the acknowledged composition handoff. Final verification and
native mounted-source review passed for application commit `c0fe19d`. The B4 PR
is stacked on B2, not an independent replacement for proof/cancellation work.
No deployment or GitHub merge has occurred.

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
and per-order attempts. No external scheduler or financial RPC is added.

## Timeout and dependency boundaries

`DeliveryTimers` borrows `PostgresRepository.WithinProofs` through a
consumer-owned transaction interface. `Mutator` locks the parent first, reads
fresh offer relationships, then samples `ProofServerTime` and calls
`HasPendingDelivery` in separate statements on that same transaction. No remote
proof check, nested transaction or pool-level clock call occurs under the lock.
The database clock is authoritative for both expiry and timeout decisions. The
discovery clock is advisory; a fast process can discover early candidates but
cannot prematurely close them. Explicit `TestNow` options are only for
deterministic policy checks, not ordinary production clock configuration.

Both policies use one private `lifecycleTimerScanner` for discovery, retry pacing
and shutdown. This shares two existing policy implementations, not a scheduler
framework for future work. Separate typed sqlc adapters remain small because their
generated query parameter/result types differ. The timeout formula appears in
Go and SQL by necessity; real SQL boundary tests check their strict agreement.

A pending on-time confirmation suppresses timeout while B2 validates/retries it.
A rejected/superseded confirmation does not protect an overdue Delivering order.
If cancellation or successful confirmation committed first, the locked status
prevents timeout. Otherwise `NewChange` atomically commits Failed, actual closure
and audit time, terminal outcome and B3's zero-payable warning fact. Credit still
owns settlement. The worker never invents delivery time or resets reservations.

The candidate query uses exact numeric nanosecond comparison against PostgreSQL
microsecond timestamps, avoiding floating-point boundary drift and interval
overflow. Migration `00008_delivery_timers.sql` adds active-population partial
indexes for original expiry and delivery discovery; it does not alter B2's
proof migration `00007`. This is not a measured scale claim.

B2 owns admission, proof preexistence/immutability, recovery and effective
delivery time. Its exported predicate considers only eligible immutable pending
submissions from the assigned courier. B3 owns cancellation, financial reopening
coordination, final item location and the timeout-warning encoding already in
the dependency. B1's explicit final-assignment-before-expiry rule is unchanged.
Shared frontend refresh/proof mounting remains B2/UI-owned; no duplicate UI is
added. After the explicit final composition handoff, B4 mounts exactly two timer
workers using the existing cancellation context and WaitGroup. The wider
deadline-policy survey in issue #178 remains external.

## Verification

Test expectations come from R1.2.4/R1.6.10/R1.6.11 and the agreed behavior above, not from
reading implementation output. Run from the repository root:

```sh
make fmt-check
make lint
(cd order-service && GOWORK=off go test -mod=readonly -race ./... -count=1)
(cd order-service && env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order -run '^TestPostgres(ExpiryTimers|DeliveryTimers|ProofReceipts)$' -count=1 -v -timeout=10m)
```

The PostgreSQL command uses a disposable PostgreSQL 18 testcontainer with random
ports. Do not set `TEST_DATABASE_URL` to a shared database: the existing fixture
resets its schema. The test covers 50 competing expiry mutations, exact durable
audit/outcome counts and preserved financial/deadline observations; 52 overdue
orders with an injected transaction failure, next-page progress and restart
recovery; and an expiry mutation waiting on a real assignment row lock before
rereading state. New timeout checks exercise durable B2 admission, actual deadline
crossing with unavailable storage inspection, restarted proof recovery, invalid
proof release, accepted replay after completion, cancellation precedence, and
50 competing timeout mutations with one warning/outcome. Object storage/decoding
are explicit test seams. These checks do not exercise a browser, live S3, live
Credit settlement, Kafka consumption or actual payout/refund.

### Historical expiry verification

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
That expiry snapshot stayed unchanged through its review. Independent native Opus 5.5 review
found no confirmed bugs and accepted retaining this isolated slice as a declared
partial implementation. Actual `generation_model = claude-opus-5-5-medium` was
verified in its native export, together with read/grep/find-only tools and
unchanged hashes for all six application/query/test files. This is not full
feature or integration approval. Private native exports are not shared artifacts.

At that historical pass, warnings concerned clock synchronization, no dedicated
index/scale evidence, large-backlog latency, missing dedicated discovery-failure
tests and a duplicate-channel-close flake risk in a blocked-attempt fake.
The current integration adds partial indexes and makes that fake's startup
signal one-shot. Timeout decisions now use B2's locked database clock. Discovery
latency under a large backlog and a production scale benchmark remain explicit
limits; an index alone is not scale evidence.

### Integrated verification

First integrated round passed root formatting/lint, all Order Service tests with
the race detector, and all sixteen PostgreSQL scenarios across ExpiryTimers,
DeliveryTimers and ProofReceipts. Native independent Standards and Spec reviews
both used verified actual `generation_model = claude-opus-5-5-medium` on thirteen
unchanged hash-pinned B4 files. Neither found a confirmed timer bug or hard
standards violation. These observations apply to the first integrated snapshot,
not automatically to later review-driven changes.

Review dispositions for the second/final pass:

- Share the duplicated expiry/timeout scanner and test discovery recovery and
  cancellation once against that actual shared implementation.
- Make clock overrides explicitly test-only and use the same locked DB clock for
  both policies. Discovery can still be delayed by a slow instance clock.
- Add a real PostgreSQL pending-admission lock wait across the deadline, synthetic
  real-SQL equality/fractional-microsecond checks, and an independent assertion
  that pending invalid-proof input is protected before definitive rejection.
- Make the blocked-attempt test signal one-shot and expand the noted long literals.
- Keep typed SQL adapters and language-specific deadline expressions rather than
  inventing a generic query abstraction. Their behavior is checked at boundaries.
- The schema already excludes null couriers for Delivering and invalid durations;
  no speculative corruption-repair behavior is added. Missing pickup times fail
  closed. Large pending populations can contend with recovery; there is no scale
  or outage-load benchmark claim.

`cmd/server/timers.go` supplies a behavioral startup/shutdown seam for the two
actual Run loops with the service's existing context and WaitGroup. The single
acknowledged call is now mounted next to proof recovery in `main.go`, before
HTTP authentication bootstrap, with cancellation/join before database-pool close.
Final verification/review includes the mounted source; results are recorded below.

The final-core native Standards/Spec re-review verified the actual Opus 5.5 model,
read-only executed tools and all seventeen unchanged source hashes. It found no
confirmed spec bugs. Corrected the remaining misleading interface/test comments
and compressed test literals. Added a real expiry database-clock regression with
candidate discovery deliberately one year ahead. Final mounted-source review
checks that small delta and production lifetime against the final B2 exporter.

Admission protection begins with a durable database receipt under the order lock,
not a client tap or raw network arrival. A request queued behind the lock can
still miss admission. The separate broader deadline-policy issue is not closed.

### Final mounted result

Application commit `c0fe19d` passed:

- Root `make fmt-check` and `make lint`, with zero lint issues.
- All Order Service packages with `go test -mod=readonly -race ./... -count=1`,
  including the executable two-worker startup/shutdown test.
- All 23 disposable PostgreSQL 18 scenarios: six expiry, six delivery timer and
  eleven concrete proof-receipt cases. They include the actual admission lock
  wait across the deadline and database-clock expiry with fast discovery.

The final native review combined Standards and Spec for the mounted delta,
reusing the earlier exact-source two-axis reviews. Verified actual
`generation_model = claude-opus-5-5-medium`, executed tools `read` (18), `grep`
(6), `find_file_by_name` (1), and all eighteen source hashes unchanged through
review and final verification. There were no confirmed runtime/spec defects.
The sole documentation finding was the old startup-status text; it is corrected.
Raw exports and execution logs remain private, not public artifact links.

These are application-to-real-PostgreSQL integration and worker-lifetime tests,
not a full production HTTP/browser/process-restart test. Storage inspection and
image decoding in the timer integration tests are controlled seams. Real cloud
storage, Kafka delivery/consumption, Credit settlement and production rollout
were not exercised. No throughput, large pending-population or outage-load
benchmark was run. A slow discovery clock can delay candidates; final decisions
still use the locked database clock. Malformed persisted rows are logged and
retried, not automatically repaired.
