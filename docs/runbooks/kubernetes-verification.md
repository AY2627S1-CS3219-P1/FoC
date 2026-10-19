# Kubernetes bootstrap verification

Verified locally on 2026-10-20: Linux amd64, Docker, kind 0.33.0,
Kubernetes 1.35.0 and Istio 1.31.2. No commits were made.

## Passed checks

- `make fmt`, `make lint`: zero lint issues across all four Go modules.
- `GOWORK=off go test -mod=readonly ./...` in `pkg`, `user-service`,
  `supplier-service` and `order-service`.
- Focused race tests for shared server/authentication, User router/server,
  Supplier server/health and Order server.
- User database integration tests against a newly created, isolated
  `foc_k8s_verification` database; Supplier and Order tagged integration suites.
- Frontend type checks, 50 tests, production build, development-server smoke,
  and production-image checks with two different runtime public origins.
- Production Go image builds for User, Supplier and Order; frontend and Kafka
  initialization image builds.
- Local and production-template manifest rendering; Kubernetes server-side
  validation; operator script safety tests (`make k8s-test`).
- Compose configuration validation and startup, frontend availability,
  healthy User/Supplier/Order RPCs, healthy PostgreSQL/Kafka/Mailpit, and
  successful Kafka topic initialization. Compose and its existing Dockerfiles
  have no changes.
- Kubernetes initial deployment, repeat `make k8s-up` with already built
  images, and independent Supplier deployment.
- All provisioning/migration/topic Jobs complete with native sidecars.
- Real gateway HTTPS frontend and public health return 200; internal User key
  RPC returns an application 404 through the public gateway.
- Authenticated application workload obtains internal keys without a user
  JWT; gateway and foreign mesh identities receive 403 internally.
- Plaintext and raw pod-IP requests cannot bypass protected listeners;
  destination proxy configuration requires client certificates.
- Admission rejects injection-disabled/fake-proxy and privileged-extra-init
  Pods; a genuine injected Pod is accepted.
- `bash scripts/k8s/proxy-startup-smoke.sh`: an unavailable proxy exhausts its
  startup probe and restarts; the application never starts.
- Real Mailpit delivery, bootstrap superadmin magic-link login, secure refresh
  cookie attributes, protected User/Supplier calls, refresh rotation, logout
  and revoked refresh rejection. Used the local-only `admin@example.test`
  bootstrap account, not an existing Compose account.
- Kafka UI redirects unauthenticated callers, accepts its separate operator
  credentials and reports the configured `foc` cluster ONLINE with one broker.
- `bash scripts/k8s/persistence-smoke.sh`: PostgreSQL retains the same bootstrap
  identity and Kafka retains an acknowledged record after both StatefulSet
  pods are replaced. PVCs were retained. A uniquely named
  `foc.infrastructure-smoke-*` test topic remains in the local broker.
- Generated clients successfully call all three services through the deployed
  TLS gateway over both Connect HTTP/1.1 and native gRPC:

  ```sh
  # In pkg/, with a gateway port-forward active on 18443:
  RPC_SMOKE_BASE_URL=https://localhost:18443 \
    RPC_SMOKE_CA_FILE="$PWD/../.local/k8s/secrets/tls.crt" \
    GOWORK=off go test -mod=readonly ./server -run TestMeshGateway -count=1
  ```

## Existing-environment recovery

The existing Compose PostgreSQL volume lacked `order_dev`. Running the
repository's existing idempotent `init-dbs.sh` created only the missing database;
no volume or existing database was reset. See the
[database landmine](../../LANDMINES/compose-existing-databases.md).

Compose had also created a root-owned `.local` directory. Only `.local/k8s`
was created with developer ownership; Compose signing keys were untouched. See
the [directory ownership landmine](../../LANDMINES/compose-owned-local-directory.md).

## Boundaries

This verifies deployment infrastructure and the implemented services, not full
D3 completion. Credit remains a placeholder and the integrated registration /
credit / order asynchronous business workflow is not implemented here. The
production overlay is a template requiring real hostname, registry, TLS/SMTP
credentials, storage and operational settings. CI was added but has not run on
GitHub; its underlying commands were exercised locally.
