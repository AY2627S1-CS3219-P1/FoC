# Immediate suspension: verification and remaining gates

Review base: `9044eccbb19488030ca1840aa2425d67fc318037`, preserving B3 PR #179.
See [the architecture decision](../adr/immediate-suspension.md).
Production authenticated dispatch remains disabled pending
[#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29).

## Current-source checks

Pinned sqlc 1.30.0 regenerated Order bindings. Pinned golangci-lint 2.14.0 was
installed with checksum verification retained and `make fmt` completed.
Repository formatting/lint, all four module suites and shared auth race tests
passed on the second verification round. User's PostgreSQL schema, store and
mounted-router race suites and its service race suite also passed. Order's
existing suspension-removal/fence tests, new account-fence regression and mounted
RPC race suites passed.

The Order domain race suite failed only in the new P2 regression's read helper:
it called `FindAssignment` without first locking the parent and reading its
relationships. It stopped before applying suspension, so P2's new end-to-end
scenario is not certified. The helper was corrected after the final allowed
round and was not rerun. No production logic changed after that round.

The first round found an unchecked response-body close and an invalid test-command
flag; it did not execute tests. Both were corrected before the second round.
The two-round limit is exhausted. Earlier stopped compilation jobs and
pre-remediation passes do not certify this source.

Tested source manifest SHA-256:
`1d2ff7b7c3ac5a84f667297151e93e9855f415721726c542a4bcbc14a44647f1`.

Final source manifest after the unverified helper correction:
`e8ff161d0e765d8caad4793fb9b586ce0b7387447c824619440bd9ec3c4ed4ee`.
Only that test file differs from the tested source manifest.

Checks use Go 1.27.0, `GOMAXPROCS=2`, `go test -p=2`, existing module dependencies,
`GOWORK=off`, `-mod=readonly` and a writable build cache. User and Order tests
require separate disposable PostgreSQL 18 databases because their goose ledgers
and destructive fixtures are independent. Never run these against shared data.

## Regression evidence

- **F1/F2/F5:** `TestAccountSuspensionReviewDatabase` combines terminal history,
  already-reverting attempts, a legacy assignment without matching identity and a
  valid removable assignment. Suspension must commit its account fence, forbid
  pickup, avoid speculative reversal and preserve existing compensation leases.
  Discovery must exclude unrelated terminal history.
- **F3:** `TestRoleDispatchReviewDatabase` exercises revisions 1-3, applied and
  rejected decisions, rollback refusal and retained identity. Migration 00008
  protects revision tombstones without rewriting migration 00007. The mounted
  internal RPC regression and `TestAccountSuspensionFenceDatabase` verify that
  stale decisions cannot receive a successful acknowledgment.
- **F4:** `TestRejectedRoleDecisionIsVisibleAndNotRetried` checks definite
  rejections remain visible, immutable and unfinalized without repeated dispatch.
  The persistence regression checks rejected rows are excluded from recovery but
  continue to block replacement and unsafe startup. Existing lost-reply recovery
  tests retain retryable intent and finalize once after acknowledgment.
- **P1:** `TestDeletedTargetDispatchRetainsHistoryWithoutBlockingStartup` uses
  actual soft deletion and a scan limit of one. Deleted-target intent must remain
  historical, neither block startup nor starve a live target, and prevent rollback
  from erasing revisions. `TestDeletedRoleTargetIsNotDispatchedOrFinalized` checks
  deletion before dispatch and during an already-sent effect.
- **P2, unverified after fixture correction:**
  `TestSuspensionDoesNotResurrectInsufficientCreditOfferDatabase` starts an
  insufficient-credit compensation, suspends, reinstates before recovery and
  recovers. The original accepting offer must stay void, with the existing
  financial attempt unchanged by suspension. A new offer after reinstatement
  must still assign through a new attempt.
- **F6:** startup intentionally fails closed for live targets with unfinished or
  rejected decisions when authenticated transport is absent. Deleted targets are
  excluded without erasing history or pretending Order acknowledged the decision.

Existing database tests also cover raised-reward restoration, unchanged expiry,
late Adjust generation fencing, duplicate recovery, replacement assignments and
pickup-first custody. Mounted User tests exercise durable handoff, fresh roles,
admin reason validation and audit. Mounted internal RPC tests require both guards
and reject unintended fixture callers. These use scripted Credit or test-only
role effects, not production service authentication.

## Reproduction

Run repository `make fmt-check` and `make lint`. Within each of `pkg`,
`user-service`, `supplier-service` and `order-service`, run:

```sh
GOWORK=off GOMAXPROCS=2 go test -mod=readonly -p=2 ./... -count=1
```

Set `SUSPENSION_TEST_DATABASE_URL` only to a disposable Order database and run
Order's `./internal/order ./internal/rpc/order ./internal/rpc/orderinternal` with
`-race -count=1`. Set `TEST_DATABASE_URL` to a different disposable User database
and run `./internal/models`, `./internal/store` and `./internal/router` serially
with `-race -count=1`. Do not give User's database URL to a concurrent `./...` run:
its package fixtures reset shared tables. Run User `./internal/service` and
shared `./auth/...` race tests separately without database URLs.

## Remaining gates

Integration-tag container suites, real Credit settlement, live Kafka/mesh behavior,
B2 proof, B4 deadlines and deployment are not certified by these checks. No new
credentials, access policy or production transport was introduced. Missing
assignment identity and definite decision rejection remain explicit reconciliation
cases, not permission to infer financial identity or replace immutable intent.

Before publication, the parent must rerun the corrected P2 regression on the final
source and assess any resulting failure. No dependency or access denial occurred
in the completed checks. Integration-tag suites were not run.

Devin CLI Opus requested changes on the review base. Parent-owned, source-pinned
Opus follow-up and publication remain required. No remediation-complete, ready-PR
or production completion claim is made.
