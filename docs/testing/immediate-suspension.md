# Immediate suspension: verification and remaining gates

Current source is based on PR #179 at
`dea9c7c78130853793e8e5a8774f607cace02256`.
See the [implementation boundaries](../adr/immediate-suspension.md).
This is partial implementation, not a completed or reviewed suspension feature.

## Passed on the pinned B3 source

With Go 1.27.0 and writable caches:

```sh
export PATH=/tmp/go/bin:$PATH
export GOCACHE=/tmp/foc-go-build GOMODCACHE=/tmp/foc-go-mod
```

- In `pkg`: `GOWORK=off GOPROXY=off go test -mod=readonly -race ./auth/httpauth`.
  Same-token role changes, stale admin authority, malformed profile/error handling,
  identity preservation, bearer-only forwarding and redirect rejection passed.
- In `order-service`: `GOWORK=off GOPROXY=off go test -mod=readonly -race
  ./internal/order ./internal/rpc/order -count=1`, with
  `SUSPENSION_TEST_DATABASE_URL` pointing to disposable PostgreSQL 18.
  Both domain and mounted RPC suites passed. The dedicated PostgreSQL test ran
  and passed; it was not skipped. Full integration-tag tests are separate below.
- In `order-service`: `GOWORK=off GOPROXY=off go vet ./internal/order ./internal/rpc/order`.
- In `user-service`: `GOWORK=off GOPROXY=off go test -mod=readonly -p 1 -race
  ./internal/router -run TestAdminSuspensionReachesExistingSession -count=1`,
  with `TEST_DATABASE_URL` pointing to a separate disposable User database.
  The mounted admin/profile test passed again on PR #179's source. User and Order
  require separate databases because their goose migration ledgers are unrelated.
- `make sqlc` using sqlc 1.30.0 generated migration/query bindings. Generated
  sources were not hand-edited. Changed handwritten Go files were gofmt formatted;
  `git diff --check` passed. Pinned repository lint remains unverified.

`TestSuspensionRemovalDatabase` uses the production PostgreSQL repository,
aggregate, assignment admission and cancellation recovery with scripted Credit.
It applies all migrations, assigns a raised reward, admits the exact suspension
target, proves repeat admission does not call Credit, and proves pickup is fenced.
It checks uncertain reversal remains pending, restart recovery restores the
original reward/reservation and expiry, delayed removal preserves replacement B
and a later assignment of A without another reversal, and pickup-first preserves
custody. Migration 20261020140000 refuses downgrade while removal is pending and supports
down/up after recovery. This focused test has no Testcontainers dependency and
requires an explicitly supplied disposable database URL; no module dependency
was replaced or changed to run it. The first attempt ran before PostgreSQL was
ready and failed to connect; after readiness, the final run passed.

The in-memory requirement test independently checks pending/restart/replay and
replacement behavior through the domain API. Suspended initial pickup is denied;
recorded pickup repeats and suspended requester receipt remain available.

## Checks blocked or not run

The broader integration command was attempted:

```sh
cd order-service
GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order \
  -run 'Suspension|PickupAndSuspension' -count=1 -timeout=6m
```

It stopped during setup, before tests ran, because the dependency already pinned
by PR #179, `github.com/klauspost/compress@v1.19.2`, returned `Forbidden` from the
Go proxy redirect on `storage.googleapis.com` (object prefix
`7c2def5c0e5e157b-github.com:klauspost:compress-v1.19.2.zip`). This new-version
request was distinct from the earlier main-base denial below. It was not retried
through another route; no dependency version was changed. The concurrent
pickup/removal integration test and the updated cancellation-migration regression
are authored but have not run. Dedicated PostgreSQL evidence above does not make
this blocked suite pass.

Earlier, on the preserved main-based auth commit `f1b8957`:

- Auth race tests, full User tests against disposable PostgreSQL, standard
  Supplier/Order suites, and Supplier auth/admin/discovery race tests passed.
  These are historical evidence on that base, not a full rerun on PR #179.
- Full `pkg` suite was blocked by denied downloads of
  `github.com/klauspost/compress@v1.15.9` and
  `github.com/pierrec/lz4/v4@v4.1.15` from `storage.googleapis.com`.
- `make fmt` could not install pinned golangci-lint 2.14.0 because
  `github.com/MirrexOne/unqueryvet@v1.5.4` returned `Forbidden` from the same host.
  Pinned format/lint checks were not certified and those downloads were not retried.
- B4/B5 GitHub issue searches returned `Forbidden` from
  `https://api.github.com/graphql`; supplied decisions and fetched source were
  used, without representing those discussions as reviewed.

## Remaining completion gates

No authorized admin-decision dispatcher or common User/Order ordering boundary
has been wired. Consequently there is no end-to-end proof of immediate automatic
removal, a suspension committing between preflight and pickup, or already-admitted
assignment fencing. The internal removal method must not be exposed as a general
manual admin operation. B2 proof, B4 deadlines, live Credit idempotency/settlement,
Kafka, browser and deployed suspension behavior remain uncertified.

The required Devin CLI Opus review has not run: no supported executable/connector
is available. No substitute model was launched. Findings cannot be claimed
addressed without that review; no ready PR, push, merge or deployment occurred.
