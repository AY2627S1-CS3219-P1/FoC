# Immediate suspension: verification and remaining gates

Depends on B3 PR #179. See [the architecture decision](../adr/immediate-suspension.md).
Local checks passed for the implementation in `b2a2f63`.
Production authenticated dispatch remains disabled pending
[#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29).

## Verified checks

Order bindings were regenerated with sqlc 1.30.0. Repository `make fmt-check` and
`make lint` passed with golangci-lint 2.14.0. All four module suites passed:
`pkg`, `user-service`, `supplier-service` and `order-service`.

Race checks passed for shared auth, User service logic, User PostgreSQL
schema/store/mounted-router suites, and Order's full `./internal/order`,
`./internal/rpc/order` and `./internal/rpc/orderinternal` suites. The Order run
included `TestSuspensionDoesNotResurrectInsufficientCreditOfferDatabase`.

Checks used Go 1.27.0, `GOMAXPROCS=2`, `go test -p=2`, `GOWORK=off`,
`-mod=readonly`, existing module dependencies and separate disposable PostgreSQL 18
User and Order databases. No dependency or access denial occurred in these checks.

## Account fences and existing compensation

`TestAccountSuspensionReviewDatabase` combines terminal history, already-reverting
attempts, a legacy assignment without matching identity and a valid removable
assignment. It verifies that suspension commits the account fence, forbids pickup,
avoids speculative reversal and preserves existing compensation leases. Discovery
excludes unrelated terminal history.

`TestSuspensionRemovalDatabase` and `TestAccountSuspensionFenceDatabase` cover
raised-reward restoration, unchanged expiry, late Adjust generation fencing,
duplicate recovery, replacement assignments and pickup-first custody. They also
verify stale-profile create/offer/accept denial and rejection of stale decisions
without a successful acknowledgment.

## Immutable role decisions and recovery

`TestRoleDispatchReviewDatabase` exercises revisions 1-3, applied and rejected
decisions, rollback refusal and retained identity. Migration 00008 protects
revision tombstones without rewriting migration 00007.

`TestRejectedRoleDecisionIsVisibleAndNotRetried` verifies that definite rejections
remain visible, immutable and unfinalized without repeated dispatch. Persistence
tests verify that rejected rows are excluded from recovery but still block
replacement and unsafe startup. `TestRoleDecisionWaitsForOrderAndRecoversSameIdentity`
verifies retryable lost-reply recovery and finalization after acknowledgment.

## Deleted targets and startup availability

`TestDeletedTargetDispatchRetainsHistoryWithoutBlockingStartup` uses actual soft
deletion and a scan limit of one. Deleted-target intent remains historical without
blocking startup or starving a live target, and rollback cannot erase its revisions.
`TestDeletedRoleTargetIsNotDispatchedOrFinalized` covers deletion before dispatch
and during an already-sent effect.

Startup intentionally fails closed for live targets with unfinished or rejected
decisions when authenticated transport is absent. This availability tradeoff remains:
User Service cannot start in that state. Deleted targets are excluded without
erasing history or pretending Order acknowledged the decision.

## Offer retirement across reinstatement

`TestSuspensionDoesNotResurrectInsufficientCreditOfferDatabase` starts an
insufficient-credit compensation, suspends, reinstates before recovery and recovers.
The original accepting offer stays void, and suspension does not change the existing
financial attempt. A new offer after reinstatement assigns through a new attempt.

## Authentication and transport seams

`TestAdminSuspensionReachesExistingSession` covers durable handoff, fresh roles,
admin reason validation and audit. `TestSuspensionRPCRequiresExplicitTrustedCallerBoundary`
requires both internal-handler guards and rejects unintended fixture callers,
including rejecting stale decisions without acknowledgment. These tests use
scripted Credit or test-only role effects, not production service authentication.

## Repeat the checks

From the repository root, with Go 1.27.0 and the pinned repository linter:

```sh
export GOMAXPROCS=2 GOWORK=off
unset TEST_DATABASE_URL SUSPENSION_TEST_DATABASE_URL
make fmt-check
make lint
for module in pkg user-service supplier-service order-service; do
  (cd "$module" && go test -mod=readonly -p=2 ./... -count=1)
done
(cd pkg && go test -mod=readonly -p=2 -race ./auth/... -count=1)
(cd user-service && go test -mod=readonly -p=2 -race ./internal/service -count=1)
```

Set `SUSPENSION_TEST_DATABASE_URL` to a disposable Order PostgreSQL 18 database and
`TEST_DATABASE_URL` to a different disposable User PostgreSQL 18 database. Verify
both URLs before running: these fixtures reset schemas or truncate tables. Never
use shared data. The services have independent goose migration ledgers.

From the repository root:

```sh
(cd order-service && go test -mod=readonly -p=2 -race \
  ./internal/order ./internal/rpc/order ./internal/rpc/orderinternal \
  -count=1 -timeout=5m -v)
for package in ./internal/models ./internal/store ./internal/router; do
  (cd user-service && go test -mod=readonly -p=2 -race "$package" \
    -count=1 -timeout=5m -v)
done
```

Run the User database packages serially. Do not supply `TEST_DATABASE_URL` to a
concurrent User `./...` run because separate package fixtures reset shared tables.
To regenerate Order bindings, run `make sqlc` inside `order-service` with sqlc 1.30.0.

## Remaining gates

Integration-tag container suites, real Credit settlement, live Kafka/mesh behavior,
B2 proof, B4 deadlines and deployment are not certified by these checks. No new
credentials, access policy or production transport was introduced. Missing
assignment identity and definite decision rejection remain explicit reconciliation
cases, not permission to infer financial identity or replace immutable intent.

Source-pinned Devin CLI Opus follow-up remains required before publication.
Passing local checks does not establish PR readiness or production authenticated
suspension completion.
