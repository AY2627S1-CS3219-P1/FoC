# Immediate suspension: verification and remaining gates

See [the architecture decision](../adr/immediate-suspension.md).
The current-main update wires the existing mesh boundary instead of leaving
production dispatch disabled. Verification results for this update are recorded
below. Earlier branch verification is not evidence for the integrated tree.

## Current-main update checks

Local verification passed with Go 1.27.0 and disposable PostgreSQL 18 databases:

- All eight module suites passed with `GOWORK=off`, `-mod=readonly`, `-p=2`
  and `-count=1`: pkg, User, Order, Supplier, scripts, Communication, Credit
  and Content Moderation.
- Shared auth race tests, Order suspension/RPC PostgreSQL race tests and User
  schema/store/router/service race tests passed. The lost-acknowledgment scenario
  rebuilt the coordinator, restored the original reward once and preserved a
  replacement courier against stale replay.
- Formatting and lint passed for all eight modules, with zero issues per module.
- Buf lint/RPC generation, sqlc generation and protobuf compatibility against
  the integrated main baseline passed. Local and production dependency overlays
  rendered with the User-only DENY intact.

The suites ran against main `6cd9843f`. Upstream `883a485f` was subsequently
merged without conflicts; binary diff comparison confirmed the suspension delta
was unchanged. Only verification notes were then updated. The combined head has
not received another local test run; CI remains the combined-head gate.

The final constructor return-type/nil-interface correction was included in the
lint/type checks, but its unit test was not rerun after that adjustment. Live mesh
tests require a managed local Kubernetes context; none is configured here.

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
decisions, rollback refusal and retained identity. Migration 00015 protects
revision tombstones without rewriting migration 00014.

`TestRejectedRoleDecisionIsVisibleAndNotRetried` verifies that definite rejections
remain visible, immutable and unfinalized without repeated dispatch. Persistence
tests verify that rejected rows are excluded from recovery but still block
replacement and unsafe startup. `TestRoleDecisionWaitsForOrderAndRecoversSameIdentity`
verifies retryable lost-reply recovery and finalization after acknowledgment.

Suspension and reinstatement require transport plus both service-level and
transaction-level dispatch stores. `TestMissingRootDispatchStoreFailsBeforeAdmission`
verifies that a missing service-level store creates no intent, role/audit change or
Order call, even with transport and transaction persistence configured.
`TestMissingRootDispatchStorePreservesExistingIntent` covers retry, direct apply
and recovery without changing existing intent. `TestOrdinaryRoleChangesDoNotRequireRootDispatchStore`
preserves promotion/demotion without a suspension-dispatch dependency.

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
admin reason validation and audit through separate public and internal listeners.
The internal self-profile read requires a bearer; profile mutations return 404.
`TestSuspensionPrivateRPCContract` checks private method mounting, validation, and
stale-decision rejection without a successful acknowledgment.

`TestSuspensionRPCDatabaseLostReplyAndReplacement` uses actual HTTP, migrations,
and PostgreSQL. A raised-reward assignment is suspended, the acknowledgment is
lost after commit, and a reconstructed coordinator receives repeated delivery.
It verifies one durable removal, pickup denial, original-reward restoration,
unchanged expiry, and preservation of a replacement courier against an old decision.
Credit is scripted. No mock workload header is treated as production identity.

`TestOrderRoleTransportUsesNoHumanCredentialsOrRedirects` exercises the production
private-client constructor with an expired bearer in context and rejects redirects.

The existing `make k8s-smoke` command now probes the suspension method from User,
another Order workload, a default FoC workload, ingress, and outsiders. User must
reach validation (400 for empty input without a state change); other meshed callers
must receive 403 even with forged headers. Plaintext transport must fail, and the
public Order listener must return 404. This is live-mesh evidence only when actually
run against a deployed cluster.

## Repeat the checks

From the repository root, with Go 1.27.0 and the pinned repository linter:

```sh
export GOMAXPROCS=2 GOWORK=off
unset TEST_DATABASE_URL SUSPENSION_TEST_DATABASE_URL
make fmt-check
make lint GO_MODULES='pkg user-service order-service supplier-service scripts communication-service credit-service content-moderation-service'
for module in pkg user-service order-service supplier-service scripts \
  communication-service credit-service content-moderation-service; do
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
(cd order-service && go test -mod=readonly -p=1 -race \
  ./internal/order ./internal/rpc/orderinternal \
  -run 'Suspension|Suspended' -count=1 -timeout=10m -v)
(cd user-service && go test -mod=readonly -p=1 -race \
  ./internal/models ./internal/store ./internal/router ./internal/service \
  -count=1 -timeout=10m -v)
```

Run the User database packages serially. Do not supply `TEST_DATABASE_URL` to a
concurrent User `./...` run because separate package fixtures reset shared tables.
To regenerate Order bindings, run `make sqlc` inside `order-service` with sqlc 1.30.0.

## Remaining gates

Integration-tag container suites, real Credit settlement, live Kafka/mesh behavior,
B2 proof, B4 deadlines and deployment are not certified by these checks. The new
User-only access policy and private transport wiring have not been deployed. No
credentials were provisioned. Existing pre-workflow suspensions are not automatically
backfilled into Order fences or assignment removal. Production rollout must reconcile
them using their original audit context. Missing assignment identity and definite decision rejection remain explicit reconciliation
cases, not permission to infer financial identity or replace immutable intent.

Passing local checks does not establish PR readiness or production authenticated
suspension completion.
