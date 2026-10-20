# Immediate suspension: verification and remaining gates

This change implements current-role authorization groundwork on main `f0afe82`.
It is not evidence for automatic courier removal or pickup/suspension ordering.
See the [integration boundaries](../adr/immediate-suspension.md).

## Requirement evidence

`pkg/auth/httpauth/currentrole_test.go` exercises the actual authenticator with
generated Connect clients and HTTP profile/key servers. One unchanged access
token observes current user, suspended, reinstated user, admin and super-admin
roles. Identity/session claims remain unchanged. Stale admin authority is removed.
Malformed/missing profiles, wrong subject, unknown/unspecified roles and lookup
errors fail closed; invalid JWTs never trigger profile reads. Redirects do not
receive forwarded bearer credentials.

`user-service/internal/router/suspension_integration_test.go` mounts the real
User RPC router, service and GORM stores against disposable PostgreSQL 18. It
asserts that warnings leave role unchanged, blank suspension reason is rejected,
an explicit administrator suspension records the trimmed reason and actor, and
the shared authenticator sees suspension/reinstatement through the existing
profile RPC with the same access token. This proves the persisted role-to-caller
boundary, not Order lifecycle effects or real login/refresh session revocation.

The Supplier RPC auth fixture now serves stored test profiles as well as public
keys, reflecting the new User Service dependency without changing domain rules.

## Commands and results

Use Go 1.27.0 and writable caches. This environment initially had an unrelated
`/usr/bin/go`; the matching Go toolchain was downloaded to `/tmp/go`.

```sh
export PATH=/tmp/go/bin:$PATH
export GOCACHE=/tmp/foc-go-build GOMODCACHE=/tmp/foc-go-mod
export GOPATH=/tmp/foc-gopath GOLANGCI_LINT_CACHE=/tmp/foc-go-lint
```

- PASS: in `pkg`, `GOWORK=off go test -mod=readonly -race ./auth/httpauth`.
- PASS: in `user-service`, `GOWORK=off go test -mod=readonly -p 1 -race ./...`,
  with `TEST_DATABASE_URL` set to a disposable PostgreSQL 18 database. This
  includes schema up/down/up, store persistence and the new mounted RPC test.
  Run packages serially because integration fixtures reset the schema/data.
- PASS: in `order-service`, `GOWORK=off go test -mod=readonly ./...`.
  Database integration was not configured for that module; this is standard-suite
  evidence only, on the foundation base without dependent mutations.
- PASS: in `supplier-service`, `GOWORK=off go test -mod=readonly ./...`
  after updating the profile-aware RPC auth fixture. The initial run with the
  old key-only fixture failed; the final run passed. Database integration was
  not configured for Supplier and is not covered by this result.
- PASS: in `pkg`, `GOWORK=off go vet ./auth/...`.
- PASS: Supplier auth fixture and mounted admin/discovery RPC tests with `-race`.
- `gofmt` applied to changed Go sources; `git diff --check` passed.
- BLOCKED: full `pkg` suite, because module downloads for
  `github.com/klauspost/compress@v1.15.9` and
  `github.com/pierrec/lz4/v4@v4.1.15` returned `Forbidden` from the Go proxy's
  `storage.googleapis.com` download URLs. Other packages completed, but the
  overall command failed. No alternate download route was attempted.
- BLOCKED: repository `make fmt` (and thus pinned format/lint checks) because
  installing golangci-lint 2.14.0 could not download
  `github.com/MirrexOne/unqueryvet@v1.5.4` (`Forbidden` from the same host).
  An earlier cache-path error was resolved with writable `GOPATH`; access denial
  was not bypassed. `gofmt` is not a substitute claim for those pinned checks.

No Supplier/Order full database integration, live Credit recovery/settlement,
proof storage, Kafka, browser or deployed suspension behavior is certified.
No push, merge or deployment was performed. Outstanding product gates remain:
automatic pre-pickup removal and financial recovery, new request/acceptance/pickup
denials through integrated mutations, already admitted assignment fencing, both
pickup race orderings, post-pickup proof/cancellation/final location, retained
requester receipt and normal B4 deadlines. Those need the integrated B1–B4 base.
Also required: a delayed or repeated suspension-removal instruction for courier
A's specific assignment cannot remove replacement courier B (or a later A
assignment) and cannot repeat credit changes. This assignment-fencing acceptance
case is documented but not implemented or tested on this foundation base.
