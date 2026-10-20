# Immediate suspension: verification and remaining gates

Base: B3 PR #179 at `dea9c7c78130853793e8e5a8774f607cace02256`.
See [architecture, fail-closed production state and ownership](../adr/immediate-suspension.md).
Authenticated production dispatch is deferred to existing
[#29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29).

## Passed checks

Go 1.27.0 with writable caches was used:

```sh
export PATH=/tmp/go/bin:$PATH
export GOCACHE=/tmp/foc-go-build GOMODCACHE=/tmp/foc-go-mod
```

- `user-service`: `GOWORK=off GOPROXY=off go test -mod=readonly -p 1 -race ./...`,
  with `TEST_DATABASE_URL` set to a dedicated disposable PostgreSQL 18 User DB.
  Schema up/down/up, stores, logic and mounted admin/profile tests passed.
- `order-service`: `GOWORK=off GOPROXY=off go test -mod=readonly -race
  ./internal/order ./internal/rpc/order ./internal/rpc/orderinternal -count=1`,
  with `SUSPENSION_TEST_DATABASE_URL` set to a separate disposable PostgreSQL 18
  Order DB. Both focused database tests ran and passed; they were not skipped.
- `pkg`: auth HTTP middleware race suite passed on the pinned source.
- User `go vet ./...` and Order `go vet` for domain, routers and both affected RPC
  packages passed with `GOWORK=off GOPROXY=off` and the writable caches.
- `make sqlc` with sqlc 1.30.0 regenerated the query/schema bindings.
- Repository `make buf-lint buf-generate BUF=/tmp/foc-buf` passed with Buf 1.73.0
  and a writable Buf cache. Go, Connect, TypeScript and Kafka output was generated;
  no generated source was hand-edited.
- `buf breaking` against pinned PR #179 passed using the local Git base.
- Frontend `npm run check` passed with zero errors and zero warnings after the
  generated contract update. No frontend business behavior was changed.
- Changed handwritten Go files were gofmt formatted; `git diff --check` passed.

User and Order need separate databases: their goose migration ledgers are
independent. Initial tool cache-path/readiness mistakes were corrected locally;
these are not passing claims for unavailable broader suites below.

## Requirement evidence

`TestSuspensionRemovalDatabase` uses production migrations, repository, aggregate,
assignment operations and B3 recovery with scripted Credit. It proves raised-reward
assignment removal, pickup fencing, pending state after uncertain reversal,
restart recovery, restoration of original reservation/reward and original expiry,
delayed/duplicate A-removal preserving B and a later A assignment without another
Credit call, pickup-first custody preservation, and migration rollback guards.

`TestAccountSuspensionFenceDatabase` proves authoritative decision installation,
automatic discovery/capture of the current assignment, fail-closed rollback when
Credit recovery is unavailable, stale-profile create/offer/accept denial, denial
when a requester accepts a suspended courier's old offer, duplicate decisions
without Credit dispatch, reinstatement versioning and old-decision replay safety.
It pauses an actual scripted Adjust after the remote boundary, applies suspension,
then releases the late reply: the old worker cannot assign; existing B1 recovery
compensates the immutable attempt once. A suspension after pickup preserves the
Delivering order and its courier.

The focused PostgreSQL tests use an explicit disposable database without importing
Testcontainers; no module dependency was replaced to run them. The broader
integration-tag concurrency test remains separately blocked, so these tests are
not described as a pass for that suite.

User's mounted admin/profile test proves warnings do not suspend, blank reason is
rejected, reason/actor are audited, same-token role changes are observed, a lost
Order acknowledgment retains durable intent without prematurely finalizing User's
role, restart recovery reuses the same identity, and completed decisions do not
redispatch. Order effects in this User-module test are a test-only acknowledgment
seam; this does not certify a real cross-service authenticated transport.

Unit tests prove the source role is finalized only after Order effects, changed
pending intent is refused, missing configured effects admits neither role nor
outbox changes, reinstatement increments the revision, and client acknowledgment
must match the original decision ID. The client does not forward an expired admin
bearer or persist credentials.

Mounted generated internal RPC tests inject a temporary workload identity only
in `_test.go`. Missing/admin/Order/Credit identities are denied; the intended
fixture identity can retry without a human token. Missing authentication OR
missing authorization yields 503 and no operation. The production public router
returns 404 for the internal mutation. These are authentication-abstraction tests,
not Istio deployment proof. Production supplies no test identity or fallback.

## Denied downloads and unrun checks

The broader integration-tag command stopped before tests ran:

```sh
cd order-service
GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order \
  -run 'Suspension|PickupAndSuspension' -count=1 -timeout=6m
```

PR #179's pinned `github.com/klauspost/compress@v1.19.2` download returned
`Forbidden` from the Go proxy redirect at `storage.googleapis.com`, object prefix
`7c2def5c0e5e157b-github.com:klauspost:compress-v1.19.2.zip`. No retry through another
route or dependency-version change was attempted. The integration-tag concurrent
pickup/removal test and adjusted migration-6 regression are authored but unrun.

Earlier main-base checks at preserved `f1b8957` passed standard Supplier/Order
suites, full User database tests, auth race tests and Supplier auth/admin/discovery
race tests. These are historical evidence, not full reruns on this final source.
Earlier full `pkg` downloads of `github.com/klauspost/compress@v1.15.9` and
`github.com/pierrec/lz4/v4@v4.1.15` returned Forbidden from the same download host.
Pinned golangci-lint 2.14.0 installation for `make fmt` was blocked by
`github.com/MirrexOne/unqueryvet@v1.5.4` returning Forbidden there. Those downloads
were not retried; pinned repository formatting/lint is not certified by gofmt/vet.

B4/B5 issue searches and later `gh pr view 179` metadata lookup returned Forbidden
from `https://api.github.com/graphql`. No alternative API was used to bypass them.

## Unmet production and review gates

Service-auth work is deferred to #29. #167's broad namespace policy is not proof
of User-only authorization. Production deliberately refuses suspension/reinstatement
admission and does not mount the internal endpoint until the approved transport
and trusted-caller boundary are supplied. No credentials, live access or policies
were created/changed. No live deployment, mesh enforcement, real Credit fencing
or settlement, Kafka delivery, B2 proof storage, B4 deadlines or browser flow is
certified. Existing requester receipt and participant cancellation rules are
preserved; this is not proof of absent B2/B4 implementations.

The required Devin CLI Opus review has not run: no supported executable/connector
is available. No substitute model was launched; no ready-PR/review completion is
claimed. Publication status and any exact denied operation are reported separately.
