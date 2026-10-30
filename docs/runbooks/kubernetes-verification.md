# Kubernetes bootstrap verification

Verified on 2026-10-21 (Asia/Singapore), on Linux amd64 with Docker 29.7.2,
Bun 1.4.3, kind 0.33.0, Kubernetes 1.35.0 and Istio 1.31.2.

Implementation revision: `783398da2d76d899e7dc14696d9b949219898a40`.
It includes main `581cf065f5943358b2ae45352867561b75b72a18`, merged through
`953c523`. Final operator, lint, Go, gateway/auth and object-storage checks were
rerun against that implementation revision. The full bootstrap, persistence,
proxy and frontend checks below were performed during its implementation.

## Bootstrap and developer tooling

- A separate, empty `foc-pr167-verify` kind cluster completed a full
  `make k8s-up`, including production image builds/load, mesh installation,
  private credentials, database provisioning, migrations, Kafka topics and
  local bucket initialization. All seven initialization/provisioning Jobs
  completed and all application Deployments became ready.
- Upgraded the existing `foc` cluster and repeated bootstrap without replacing
  its PVCs or rotating its signing keys/passwords. The replacement multiarch
  PostgreSQL image retained PostgreSQL 18.6, PostGIS 3.6.4 and existing data.
- Repeated `make k8s-up` using HTTPS port 19643, Kafka UI port 19680 and Mailpit
  port 19625. All forwards worked; frontend runtime configuration used the new
  origin for every browser RPC service. Ctrl+C released all three ports.
- `make k8s-test`: 22 passing tests, including strict TypeScript compilation,
  all local/production Kustomize stages, warm tool-cache reuse without network,
  `.env` loading/export precedence, credential preservation, portable TLS
  certificate generation, transient apply recovery and Ctrl+C cleanup during
  both forward startup and steady operation.
- A real transient `etcdserver: request timed out` response occurred during a
  repeat deployment. Applying the same desired resources recovered without
  deleting active Jobs. Bounded retries now cover this case; a regression test
  also confirms permanent errors are not retried.

## Application and mesh boundaries

- `make fmt`, `make lint`: zero lint issues across `pkg`, User, Supplier,
  Order and Communication. `GOWORK=off go test -mod=readonly ./...` passed in
  each of those five modules.
- Fresh-cluster and retained-cluster `make k8s-smoke` passed: Kubernetes DNS
  discovery, authenticated internal User key retrieval, gateway/foreign mesh
  identity rejection, plaintext/raw pod-IP rejection and STRICT destination
  mTLS configuration.
- Admission rejected injection-disabled/fake-proxy and privileged-extra-init
  Pods. Genuine injected Pods were accepted.
- Trusted gateway HTTPS served the frontend and all four service health RPCs.
  The internal key procedure returned an application 404 publicly, rather
  than frontend HTML. Gateway access logs retained paths without the query
  canary used to detect credential leakage.
- Generated clients called User, Supplier, Order and Communication through the
  deployed TLS gateway over both Connect HTTP/1.1 and native gRPC:

  ```sh
  # In pkg/, with a gateway forward on 19643:
  RPC_SMOKE_BASE_URL=https://localhost:19643 \
    RPC_SMOKE_CA_FILE="$PWD/../.local/k8s/secrets/tls.crt" \
    GOWORK=off go test -mod=readonly ./server -run TestMeshGateway -count=1
  ```

- Real Mailpit delivery and the local `admin@example.test` bootstrap account
  exercised magic-link login, secure cookie attributes, protected User/Supplier
  calls, Order list and Communication link-status calls, refresh rotation,
  logout and rejection of the revoked refresh session. Telegram remained
  disabled; no external bot polling was needed.
- `make k8s-storage-smoke` passed real signed HTTPS PUT/GET, private metadata
  inspection, unsigned/tampered request rejection and private cleanup. Valid
  header-signed HEAD requests could inspect an object but received 403 at the
  bucket root. No test object was retained on success.
- `make k8s-persistence-smoke` acknowledged and read a Kafka record before
  replacing the PostgreSQL/Kafka pods, then verified both the bootstrap
  identity and exact record afterward. PVCs were retained; a uniquely named
  `foc.infrastructure-smoke-*` topic remains as test evidence.
- `make k8s-proxy-startup-smoke` observed an unavailable proxy exhaust its
  injected startup probe and restart while the application never started.
  Its observation budget now includes probe timeout duration.

## Compose and frontend regression checks

- Frontend dependency installation, type checks (zero errors/warnings), 71 tests
  across 14 files and production build passed. The build still reports existing
  circular dependency/browser-module warnings.
- `docker compose config --quiet` passed. Rebuilt/restarted the original
  User/Supplier/Order development services with existing volumes; their health
  RPCs and the development frontend returned HTTP 200. PostgreSQL, Kafka and
  Mailpit remained healthy.
- Compose configuration and development Dockerfiles are unchanged relative to
  the merged main revision. Kubernetes tooling has its own root Bun lockfile;
  frontend npm dependencies and the Compose bootstrap commands remain separate.

## Resulting local state

Only the disposable `foc-pr167-verify` cluster was deleted. The original `foc`
cluster and Compose data remain. Temporary forwards were stopped, and the
retained Kubernetes cluster was redeployed with its default
`https://localhost:5173` origins; all workloads and seven Jobs were ready or
complete. Existing Compose still occupies port 5173, so choose alternate
forward ports when running both deployments together.

For older Compose volumes lacking service databases, use the existing
[idempotent database recovery](../../LANDMINES/new-service-database.md), never
`docker compose down -v`. See also the
[local-directory ownership landmine](../../LANDMINES/compose-owned-local-directory.md).

## Coverage limits

OCI indexes were inspected for native amd64/arm64 images. Native arm64 and
macOS execution were not available locally. CI now includes native Linux
amd64/arm64 deployments and macOS installer/operator checks; these GitHub jobs
have not been run as part of this local verification. macOS Docker deployment
is not covered by the macOS tooling job.

At the implementation revision above, detailed per-service Istio authorization
was deferred and authenticated FoC namespace workloads communicated broadly.
User-context JWT checks and the public/internal listener boundary remained
enforced.

At that revision, Credit had schema/contracts but no executable backend; its
reserved public route returned 503. Credit-dependent creation/settlement and the
complete asynchronous D3 business workflow were not verified or completed by
those infrastructure changes. External Telegram polling was not tested. The
production overlay is a template requiring real hostname, registry,
TLS/SMTP/AWS credentials, storage and operational settings; it was rendered, not
deployed to a production cluster.

## Devin review follow-up

Reviewed the GitHub-visible findings through review `5593087975` against
`a22bccd`; follow-up source revision is `c4c0b4e`.

- The `.env` finding is not reproducible with the pinned Bun runtime. Extended
  subprocess regressions verify the gateway port is loaded from `.env` both
  with and without an explicit exported override.
- The original custom-port bootstrap issue was already fixed, but access alone
  could still attach a different port from the deployed origins. Reproduced
  that remaining path and added a pre-forward guard covering every local
  browser origin and the public S3 endpoint. Unit fixtures and the retained
  cluster confirm that a mismatched port fails with a full-redeploy instruction.
- Reproduced Telegram handler failures being acknowledged and later updates
  being processed despite the failure. Polling now retries state changes before
  acknowledgement, with cancellation-aware backoff. Bot replies are best effort
  so a failed send cannot replay an already committed one-time redemption or
  block later unlink events. Token rejection still stops polling.
- Reproduced logout deleting an unresolved Order identity. Logout now removes
  sensitive submitted fields while retaining caller-scoped retry metadata and
  a redaction marker. Recovery blocks a fresh submission until the caller
  explicitly confirms checking My Orders and resolving the previous request.
  That confirmation clears the marker without automatically submitting anything.
- The exact-pickup finding does not match the current accepted browsing
  contract: `pickup_location_id` is an exact filter, while destination locations
  and instructions are restricted. Added a regression assertion and clarified
  the ADR; runtime pickup visibility was not changed.

Follow-up checks passed: `make k8s-test` (strict type check and 26 tests),
`make fmt`, `make lint` (all five modules), the full Communication Go suite,
Telegram poller/bot race tests, Order aggregate tests, and frontend type check,
72 tests and production build. Regression cases failed before their fixes.
External Telegram and interactive browser execution were not exercised for
this follow-up; the original full mesh/bootstrap evidence above remains scoped
to its recorded implementation revision.

## D3 merge and Credit build verification

On 2026-10-30, `origin/main` was merged into the Kubernetes branch and the
previously reserved Credit backend was added to the Kubernetes manifests. The
Credit service has its own database and migration Job, and Order-to-Credit
internal RPC is restricted by Istio identity policy. This follow-up verified
local buildability and rendered configuration; it did not deploy to a cluster
or run a live `k8s-smoke` check.

- All seven Kubernetes images built locally from their checked-in Dockerfiles:
  User, Supplier, Order, Credit, Communication, frontend and `kafka-init`.
  The frontend production build reports existing circular-dependency warnings.
- `make k8s-test` passed with strict TypeScript checks and 26 operator/config
  regression tests. `docker compose config --quiet` passed.
- Communication's shutdown cleanup test passed ten consecutive focused runs
  and its Go module suite passed after the test server gained an explicit
  release signal for its long-poll handler.
