# Kubernetes development and deployment

Kubernetes is an additional deployment option. Compose retains its development
commands, host ports, source mounts and signing keys, but existing `.env` files
must change `USER_SERVICE_BASE_URL` to `http://user-service:8081` for User's
internal public-key listener. Port 8080 continues to serve User's public APIs.
Follow the [Compose upgrade instructions](../../README.md#upgrading-an-existing-compose-setup)
and recreate the affected containers. Compose also gains `INTERNAL_PORT`
wiring for User and Credit, and Order's
`CREDIT_SERVICE_BASE_URL` now defaults to Credit's internal listener. Credit is
deployed with its own database and Istio-restricted internal
listener; this does not claim the complete D3 business workflow.

## Start locally

Setup prerequisites, the one-time aqua policy approval, tool pinning, and the
quick start command are documented in the
[Kubernetes setup guide](../../infra/k8s/README.md).

Operational behavior worth knowing once the stack is deployed:

- `k8s-up` installs the pinned, checksum-verified tools into `.tools/aqua`,
  creates/reuses kind cluster `foc`, installs the pinned Gateway API Standard
  CRDs and Istio charts, builds/loads images, and deploys the gateway,
  dependencies, database provisioning, observability, migrations/topics and applications.
  Failed Jobs stop rollout. Existing running Jobs are not deleted. Retrying
  replaces only the exact terminal initialization Jobs; databases and PVCs are
  not reset.
- The private kubeconfig is `.local/k8s/clusters/foc.config`; your current
  global Kubernetes context is not changed. Namespace `foc` holds application
  resources.
- Run `make k8s-status` to inspect readiness and warnings. Run `make k8s-smoke`
  to check the frontend, every public health RPC, the public/private endpoint
  boundary, and the User and Credit Service target ports through the Gateway.
- If Compose created a root-owned `.local`, follow the
  [directory ownership recovery](../../LANDMINES/compose-owned-local-directory.md).
  Use a consistent `K8S_STATE_DIR` override if choosing another private
  location.

## Which address belongs where?

| Caller | Address | Meaning |
| -- | -- | -- |
| Browser | `https://localhost:5173` | TLS gateway; frontend and public RPCs share one origin |
| Supplier calling User | `http://user-service-internal:8080` | Private Kubernetes DNS; Istio supplies network mTLS |
| Order calling Credit | `http://credit-service-internal:8080` | Istio mTLS identifies Order's ServiceAccount; only Order is authorized on Credit's internal port |
| Gateway calling User | `user-service-external:8080` | Private ClusterIP Service pointing only to public handlers |
| Developer opening Kafka UI | `http://localhost:8080` | Private operator port-forward; login required |
| Developer opening Mailpit | `http://localhost:8025` | Local/development mailbox port-forward |

Both `-external` and `-internal` are ClusterIP Services: private addresses, not
internet listeners. Both expose Service port 8080. The external Service targets
`PORT`; User and Credit also have internal Services that target `INTERNAL_PORT`.
Istio forwards inbound traffic
from its pod listener to the app on loopback. Names alone are not the security
boundary.

Kubernetes resolves a Service by its name inside the same namespace, or by its
full name such as `user-service-internal.foc.svc.cluster.local` across
namespaces. The app configuration uses those Service names for backend calls;
Kafka advertises `kafka.foc.svc.cluster.local`. Docker Compose provides its own
DNS for Compose service names such as `user-service`. This is standard platform
service discovery: application code receives its destinations from
configuration and does not detect Kubernetes, Compose, or a service mesh.

The browser does not resolve these cluster-only names. Its public service URLs
all use the Gateway origin, so the frontend and browser RPCs share one origin.
Locally, `kubectl port-forward` makes that origin `https://localhost:<port>`;
in an external deployment, the operator supplies the public network path and
DNS for the configured Gateway hostname. The production overlay sets an example
hostname and TLS Secret name, but this repository does not create a DNS record
or an external load balancer. The Gateway-generated Service is `ClusterIP`, so
an operator must expose it through the environment's ingress/load-balancer
setup and point DNS at that entry point.

A backend uses one internal base URL per destination for both public and
internal procedures. Do not use raw pod IPs: they are unstable, and Istio cannot
automatically choose the correct mTLS upstream for an arbitrary pod-IP request.
Strict mTLS rejects those plaintext bypass attempts.

`localhost` means the machine running `kubectl port-forward`, not necessarily
the machine hosting Kubernetes. Running the command on your laptop exposes the
forward on your laptop, even for a remote cluster. Closing the foreground
command stops access; restart a forward if its selected pod is replaced.

Browser URLs (`PUBLIC_USER_SERVICE_URL`, `PUBLIC_SUPPLIER_SERVICE_URL`),
`ORIGIN`, and User's `FRONTEND_BASE_URL` all use the browser gateway origin.
These are runtime settings in `foc-config`, not Kubernetes DNS names.
Ingress uses Kubernetes `Gateway` and `HTTPRoute` resources with Istio as the
Gateway API controller. The managed local install vendors Gateway API v1.6.0
Standard CRDs. External clusters must already serve Gateway API v1 and have
Istio installed as the `istio` GatewayClass controller; bootstrap checks for
the CRDs but does not replace them. Routes list full public protobuf service
paths because standard `PathPrefix` matching is segment based. Add a route for
each new public protobuf service. User's public-key service is internal-only
and has no Gateway route. Mesh mTLS and workload authorization remain
Istio-specific.

The local Gateway and routes omit hostnames so the local TLS certificate can
serve `localhost`; production sets the listener and routes to a real FQDN.

## TLS, identities and secrets

Trust `.local/k8s/secrets/tls.crt` explicitly in your development browser or OS
before using the HTTPS gateway. Bootstrap never changes your host trust store.
For curl use `--cacert .local/k8s/secrets/tls.crt`; do not disable verification.

Local credentials and signing keys remain in `.local/k8s/secrets` across retries.
Do not delete that directory while retaining PostgreSQL data. The generator does
not silently rotate database passwords or JWT keys. Credential files must never
be committed or printed into logs. Local values follow three patterns:
machine-generated (database passwords, the JWT key, the TLS pair, the Kafka UI
password — read them back from that directory), operator inputs for values a
machine cannot choose (`K8S_TELEGRAM_BOT_TOKEN`,
`K8S_BOOTSTRAP_SUPERADMIN_EMAIL`), and fixed non-secret constants (the Kafka UI
username, local object-storage test credentials).

For the local overlay, `make k8s-up` creates and applies Kubernetes Secrets from
that private directory. External-cluster deployments require the operator to
create the listed Secrets before deployment; the production template does not
generate or rotate credentials.

| Kubernetes Secret | Purpose |
| -- | -- |
| `postgres-admin`, `user-db`, `supplier-db`, `order-db`, `credit-db`, `communication-db` (`foc`) | PostgreSQL administrator and per-service database credentials |
| `jwt-signing` (`foc`) | User Service's JWT signing private key; mounted read-only only in User Service |
| `smtp`, `telegram`, `kafka-ui-auth`, `object-storage` (`foc`) | Mail sender, optional Telegram bot, Kafka UI login, and S3 credentials |
| `foc-local-tls` / `foc-public-tls` (`istio-ingress`) | Gateway TLS certificate and key for the local / production hostname |

The AWS overlay can select SES API delivery for Communication Service instead of SMTP.
With the verified `ses_sender_email` Terraform input, the generated overlay uses
IRSA and removes Communication's `smtp` Secret dependency. SMTP remains the default for
local and existing deployments. See infra/aws/README.md for role scope and
sender/delivery verification. User Service delegates magic-link delivery to Communication.

The JWT public key is served by User Service's internal public-key RPC for
other services to verify tokens; they do not receive the signing private key.
Kubernetes Secret objects are the deployment input, while `.local/k8s/secrets`
is the local source of truth across retries.

These are separate identities:

- FoC superadmin: application account, signs in using a Mailpit magic link.
- Kafka UI operator: username in `kafka-ui-user`, password in
  `kafka-ui-password`, under that private secrets directory.
- Kubernetes operator: kubeconfig/RBAC grants `kubectl` access.

`SPRING_SECURITY_USER_NAME` and `SPRING_SECURITY_USER_PASSWORD` configure the
existing third-party Kafbat UI, which is built with Spring. FoC remains Go and
Svelte; no Spring service was added. Kafka UI has static broker configuration
(`kafka:9092`) and dynamic configuration disabled in Kubernetes. UI login is
not Kafka topic/broker authorization. Compose Kafka UI remains unchanged and
does not gain authentication automatically.

## Superadmin and email

Provisioning creates `user_db`, `supplier_db`, `order_db`, `credit_db`, and
`communication_db`, each with a separate non-superuser role. The operator Job
installs Supplier's PostGIS
extensions. Migration Jobs run before applications; Kubernetes sets
`RUN_MIGRATIONS=false` on normal application containers.

User's existing superadmin bootstrap still runs when startup migrations are
disabled. It uses `BOOTSTRAP_SUPERADMIN_EMAIL` and optional
`BOOTSTRAP_SUPERADMIN_DISPLAY_NAME`, patched by the corresponding
`K8S_BOOTSTRAP_*` bootstrap inputs. Its transactional singleton remains in
PostgreSQL: restarts and concurrent replicas do not create duplicates, changing
the configured email does not create a second first admin, and bootstrap does
not undo a later role demotion. Never delete the bootstrap row to make a test
pass.

Bootstrap also queues the superadmin's user-created event, so Credit opens
their account with the starting credits. A superadmin bootstrapped before this
had no Credit account. Redeploy with `K8S_BOOTSTRAP_SUPERADMIN_EMAIL` set to any
address: User restarts, finds the existing bootstrap row and queues the missing
event for the original superadmin. No second admin is created and existing
balances are unchanged.

Sign in through the normal magic-link flow. Mailpit receives local SMTP at
`mailpit:1025`; its web UI is port-forwarded only. Production omits Mailpit and
requires operator-provided SMTP credentials compatible with the existing sender.
No authentication test sends mail outside the local Mailpit setup.

## Initialization jobs and demo data

`make k8s-up` and a full `make k8s-deploy` prepare the database, schema, and
Kafka before rolling out applications. These Jobs do setup work; they do not
load demo users or supplier records.

| Job | Work performed |
| -- | -- |
| `db-provision` (local overlay only) | Creates the five service databases and non-superuser roles if they are missing on the shared local Postgres, grants database access, and installs Supplier's PostGIS and `pg_trgm` extensions. It does not drop existing data. Production pre-provisions its database before deploying and runs no provisioning Job. |
| `user-migrate`, `supplier-migrate`, `order-migrate`, `credit-migrate`, `communication-migrate` | Apply each service's Goose migrations before application rollout. Normal app containers have `RUN_MIGRATIONS=false`. |
| `kafka-init` | Creates the topics declared in `infra/kafka/topics.conf`; topic auto-creation is disabled on the broker. |
| `order-storage-init` (local overlay only) | Creates the Order image bucket and browser CORS configuration in local Floci. External deployments supply their own object storage. |

The first superadmin is not a Kubernetes Job. User Service performs its
existing transactional bootstrap on startup using the configured bootstrap
email; the state is stored in PostgreSQL and survives restarts.

Demo fixtures are separate from deployment initialization. `make seed-demo`
and `make seed-demo-reset` run the Go seed command through `docker compose exec`
against the Compose PostgreSQL container. The manifest currently seeds
only `users.sql` and `suppliers.sql`. These targets are not Kubernetes Jobs and
are not run by `make k8s-up`; the repository has no Kubernetes demo-seeding
target. Adding one depends on the seeding rework in
[#212](https://github.com/AY2627S1-CS3219-P1/FoC/pull/212); until that lands,
seed only the Compose stack.

## Mesh boundary and application behavior

Applications do not have a `single` or `mesh` mode. `PORT` defaults to 8080 and
`INTERNAL_PORT` to 8081. Every service runs the listener split: public routes on
`PORT`, private routes on `INTERNAL_PORT`. User serves its public-key RPC only
on the internal listener; Credit keeps reservation RPCs on the Order-only
internal listener. Supplier, Order, and Communication do not have private routes
yet, so their internal listener currently serves the same public handler —
binding it early keeps local and deployed behaviour identical, and a later
route split becomes a handler move rather than new plumbing. Internal service
URLs may use HTTP; Istio STRICT mTLS encrypts the Kubernetes network hop.
JWT verification, roles and ownership checks remain enabled.

Internal User public keys require no user JWT, but do require mesh
authentication. They are absent from the public router (404). User APIs that
act on a user's behalf require a valid JWT on the public listener. Credit
account APIs require a user JWT; reservation calls stay on the internal
listener and are restricted to Order's mesh identity. Forward the user's bearer
token for operations performed on their behalf; background, key and health
operations without user context do not invent a JWT.

A native proxy and its bounded startup probe must be ready before an application
starts. Admission rejects missing/forged proxy setup, injection opt-outs, host
networking, capture overrides and additional privileged initializers. App port
listeners have explicit Istio ingress forwarding. Namespace-wide STRICT mTLS
and a default-deny policy apply; authenticated app-namespace workloads are
coarsely allowed, and gateway identity is explicitly denied internal port 8081.
Credit's internal port 8081 has an additional DENY policy for every principal
except `cluster.local/ns/foc/sa/order-service`. Credit's internal RPC handler does
not verify application credentials; the Istio principal check is its Kubernetes
boundary. Other fine-grained service authorization and Kafka ACLs are deferred.

If a running proxy fails, the pod becomes unready and the proxy restarts;
the application is not automatically killed. There is no direct-network
fallback. This boundary does not protect against a trusted same-pod debugger or
an administrator who can change the Kubernetes policies.

The frontend uses adapter-node in a pod. Its existing behavior is unchanged:
SSR renders a loading shell, then the browser restores its session and fetches
data. It is neither a newly converted static SPA nor authenticated data SSR.

## Debugging: the Kubernetes equivalent of docker exec

First select the private kubeconfig in the terminal used for manual commands:

```sh
export KUBECONFIG="$PWD/.local/k8s/clusters/foc.config"
kubectl -n foc get pods,jobs,pvc
kubectl -n foc logs deployment/user-service -c user-service --tail=100 -f
kubectl -n foc describe pod <pod-name>
kubectl -n foc logs <pod-name> -c user-service --previous
kubectl -n foc logs <pod-name> -c istio-proxy --tail=100
```

`-c` selects the application rather than its proxy. The distroless Go images
have no shell or psql. Order uses Debian for its native image codecs; use
installed probes for routine checks in either runtime:

```sh
kubectl -n foc exec deployment/user-service -c user-service -- /app/healthcheck ready 8080 /user.v1.HealthService/Check

# psql is installed in PostgreSQL, not User Service:
kubectl -n foc exec -it statefulset/postgres -c postgres -- psql -U foc -d user_db

# If a separate diagnostic image is approved for your environment:
kubectl -n foc debug -it pod/<pod-name> --image=<approved-debug-image> --target=user-service -- sh
```

Use actual configured database/user names if overriding local defaults.
Prefer read-only SQL while debugging. Never put passwords on command lines.

`exec`, ephemeral debugging and `port-forward` are privileged operator paths;
they can access loopback endpoints directly and are not ordinary mesh traffic.
Restrict their Kubernetes RBAC permissions in production. Use actual caller
pods and the gateway, not operator shortcuts, to verify mesh isolation.

## Updates, tests and deletion

```sh
make k8s-test
make k8s-deploy service=supplier-service
# Full deploy reruns provisioning/migrations (local overlays provision the
# shared Postgres; production pre-provisions); required for schema changes:
make k8s-deploy

# Additional explicit local tests:
make k8s-proxy-startup-smoke
make k8s-persistence-smoke
```

The persistence test briefly restarts local PostgreSQL/Kafka without deleting
PVCs, then checks the bootstrap identity and a dedicated acknowledged Kafka
record. It retains its uniquely named test topic. The proxy test uses an
isolated disposable namespace to prove that an unavailable proxy cannot start
the application. Neither test changes production security policies.

With a gateway forward active, deployed Connect and native gRPC can also be
checked from `pkg/` using `RPC_SMOKE_BASE_URL` and
`RPC_SMOKE_CA_FILE`: see the [verification commands](kubernetes-verification.md).

`K8S_SKIP_BUILD=1` reuses images already loaded/published. Use it only when
their source is current. `K8S_IMAGE_PREFIX` and `K8S_IMAGE_TAG` change application
and migration image references together. For smoke port conflicts, set
`K8S_SMOKE_HTTPS_PORT` (default 18443) and `K8S_SMOKE_MAILPIT_PORT` (19025).

`make k8s-demo` deliberately fails rather than claiming the complete D3 business
workflow works. `K8S_CONFIRM_DELETE=foc make k8s-delete-cluster`
explicitly deletes only the managed local cluster and its PVC data. It retains
credential files. Do not run it when you need the data.

For older Compose volumes missing the Order database, use the existing
[idempotent database initialization recovery](../../LANDMINES/new-service-database.md);
never use `docker compose down -v` as a workaround.

## Future deployments

`infra/k8s/base` contains portable workloads/security; local and production
overlays customize them. `overlays/production` is a template, not a
production-ready environment. Supply a real hostname, registry/tag, gateway TLS
Secret `foc-public-tls` in namespace `istio-ingress`, all application/database/UI Secrets, real SMTP, storage
capacity/class, backups, availability and resource settings. Do not generate
local Mailpit credentials for production. Kafka and Kafka UI remain in the
production template, private and meshed, with single-broker limitations explicit.

Existing-cluster deployment requires both an explicit context and overlay:

```sh
K8S_CONTEXT=<context> K8S_OVERLAY=<completed-overlay-path> \
  K8S_IMAGE_PREFIX=<registry>/foc K8S_IMAGE_TAG=<immutable-tag> \
  K8S_PUSH_IMAGES=1 make k8s-deploy
```

Install the pinned platform resources and Istio first, or use `k8s-up` with
those explicit inputs if authorized to install cluster-wide resources.
Locally, Istio itself is installed with Helm: two releases, `istio-base` and
`istiod`, rendered from the charts inside the pinned Istio archive and
configured by `infra/k8s/platform/istiod-values.yaml`; an external operator
replicates that with a compatible Istio installation.
Existing-cluster mode never creates/deletes kind or changes the global context.
Use `K8S_SKIP_BUILD=1` for already published images. The local-only origin
patch does not run against external clusters: browser-facing origins come from
the completed overlay, and the only deploy-time patch is the bootstrap input
(`K8S_BOOTSTRAP_SUPERADMIN_EMAIL`) when it is provided.

Istio initialization needs NET_ADMIN/NET_RAW. A namespace enforcing Kubernetes
Restricted Pod Security must use an appropriately configured Istio CNI setup;
the supplied, tested local profile uses Istio's standard initializer. Admission
pins that initializer/proxy shape, so an Istio upgrade must update and retest
the policy alongside the chart/version pin.

## Current-main updates

A full deployment now provisions and migrates Communication's own database on
the local overlay (production pre-provisions and only migrates),
starts its optional Telegram API, and configures Order's Supplier discovery,
Kafka outbox and local image storage. All deployed browser services use the
same HTTPS origin, including `PUBLIC_ORDER_SERVICE_URL` and
`PUBLIC_COMMUNICATION_SERVICE_URL`. Changing `K8S_HTTPS_PORT` requires a full
`make k8s-up` or full `make k8s-deploy` so runtime origins and presigned URLs
change together. `make k8s-access` rejects mismatched origins in the managed
local cluster before opening a forward.

Floci is local-only, uses native sidecars and a PVC, and enforces S3 signatures.
`order-storage-init` completes before application rollout. Object paths under
`/order-images/` are forwarded unchanged through HTTPS; bucket initialization
and deletion stay private. `make k8s-storage-smoke` requires Go and checks real
signed uploads/downloads, tampering/unsigned and bucket-root rejection, and cleanup. Its
optional ports are `K8S_STORAGE_SMOKE_HTTPS_PORT` (19443) and
`K8S_STORAGE_SMOKE_S3_PORT` (19466).

The production overlay also requires Secrets `credit-db`, `communication-db`
and `telegram` (an empty `TELEGRAM_BOT_TOKEN` disables the bot). Configure real S3
bucket/endpoints, AWS credentials and HTTPS object access in that overlay;
the local emulator and its test credentials are not included there.

For local Telegram configuration, first setup accepts
`K8S_TELEGRAM_BOT_TOKEN`. Later deployments preserve the credential file;
edit `.local/k8s/secrets/TELEGRAM_BOT_TOKEN` explicitly to rotate or clear it,
then redeploy. An empty file disables the bot. Telegram allows only one poller
per bot, so never share a token with another running poller. This matches the
other preserved local credentials.

### Image sources

| Component | Upstream source | Architectures |
| -- | -- | -- |
| PostgreSQL/PostGIS | [GUI/postgis-docker](https://github.com/GUI/postgis-docker), based on official PostgreSQL | amd64, arm64 |
| Kafka / topic initialization | [Apache Kafka](https://hub.docker.com/r/apache/kafka) | amd64, arm64 |
| Kafka UI | [Kafbat](https://github.com/kafbat/kafka-ui) | amd64, arm64 |
| Mailpit | [Axllent](https://github.com/axllent/mailpit) | amd64, arm64 |
| Local S3 | [Floci](https://github.com/floci-io/floci) | amd64, arm64 |
| Istio control plane / proxies | [Istio](https://github.com/istio/istio) | amd64, arm64 |
| Go runtime (except Order) | [Google Distroless](https://github.com/GoogleContainerTools/distroless) | amd64, arm64 |
| Order runtime / proof codecs | [Debian](https://hub.docker.com/_/debian) and [ImageMagick](https://github.com/ImageMagick/ImageMagick) | amd64, arm64 |
| Frontend runtime | [Official Node image](https://github.com/nodejs/docker-node) | amd64, arm64 |

Application runtime images contain no compiler. The frontend runs Node;
Go services use their compiled binaries. Host deployment tooling is a Go CLI
under `scripts/k8s`, built by Make into `.tools/bin/k8s`. Linux amd64/arm64 and
macOS hosts are supported; Docker Desktop is required on macOS.

The shared Dockerfile selects Order's native runtime when built with
`--build-arg SERVICE=order-service`. It builds checksum-pinned ImageMagick
7.1.2-35 with JPEG, PNG, WebP and HEIC/HEIF delegates, installs the existing
restricted proof policy, and runs `/app/proof-image-check` as UID 65532 during
the image build. A missing or unusable decoder therefore fails the build.
Order's container limits are 3 GiB and two CPUs, matching Compose's budget for
the application and its bounded native decoders. These limits are a deployment
budget, not a measured peak-memory guarantee.

Access logs intentionally omit query strings using Envoy's query-free path
formatter. This keeps presigned URL signatures and login-link tokens out of
collected logs without changing requests. The mesh smoke checks this with an
innocuous query canary.

Manifest application retries transient API timeouts up to three times with the
same desired resources. Permanent validation/authorization errors stop promptly.
If retries are exhausted, inspect `make k8s-status` and wait for any running
initialization Jobs before retrying. Running Jobs and persistent data are not
deleted as a timeout recovery mechanism.

## Kubernetes logging

The deployment stages are gateway → dependencies → database provisioning →
observability → migrations/topics → applications. Observability waits for local
Floci readiness and the idempotent `foc/loki-bucket-init` Job before installing
release `loki` in `foc-observability` with `helm upgrade --install --wait`.
Grafana and Alloy remain Kustomize resources. Do not apply the Helm-rendered
Loki resources with kubectl or add them to a Kustomization.

The chart archive is cached at `.tools/charts/loki-21.0.0.tgz`; its SHA-256 is
checked on every use against `infra/k8s/observability/chart.sha256`, copied from
[the maintained chart index](https://grafana-community.github.io/helm-charts/index.yaml).
Shared values and environment overrides live in `infra/k8s/observability`.
`loki` 3.7.9, Grafana 13.3.0 and Alloy 1.21.3 use immutable multiarchitecture
image digests. See the [architecture decision](../adr/0001-kubernetes-logging.md).

Set `K8S_GRAFANA_PORT=3000` in an existing `.env`, then use `make k8s-grafana`
or the normal `make k8s-up` / `make k8s-access` session. Blank disables its
forward; all configured ports must be distinct and free. CI explicitly sets
it blank. Open `http://localhost:3000` and use the values in the private
`.local/k8s/secrets/grafana-user` and `grafana-password` files (or the matching
`K8S_STATE_DIR`). Credentials survive redeployment and cluster recreation.
Grafana also persists its users in SQLite: changing an environment Secret does
not reset an existing database password; rotate through Grafana's admin tools
and update the preserved credential together. Never commit these files.

Loki is provisioned as the default data source. In Grafana Explore, try:

```logql
{namespace="foc",service="order-service",container="order-service"}
{namespace="foc",container="istio-proxy"} | json | response_code >= 500
{namespace="istio-ingress",container="istio-proxy"} | json
sum by (service) (count_over_time({namespace="foc"}[5m]))
```

Alloy reads only pods and pod logs in `foc` and `istio-ingress`. Application,
dependency, frontend and native Envoy lines keep their existing formats.
Labels are namespace, service, pod, container and node; user IDs, tokens and
request IDs are never extracted into indexed labels. JSON parsing in a query
happens at query time. Loki may add its standard `service_name` label derived
from `service`. Existing Envoy log policy continues to omit query strings.
There is no public Loki or log-bucket Gateway route. STRICT mTLS and exact
Alloy/Grafana identities protect Loki. Only Loki's identity receives additional
local Floci permission on the dedicated `foc-loki` bucket; existing image
permissions and gateway restrictions still apply.

Loki keeps WAL, index working files and compactor markers on a retained 5 GiB
PVC. Grafana and Alloy each have a 1 GiB PVC. Helm removal and scale-down retain
Loki PVCs. Removing the entire kind cluster destroys its local volumes.
Deleting Kustomize PVC manifests explicitly also destroys their claims; back up
Grafana before doing so. Floci's existing 2 GiB volume is shared with image
storage. Monitor disk use: seven-day retention is time-based, not a disk quota,
and compactor deletion is asynchronous. One Loki/Grafana/Alloy instance provides
no high availability and no guarantee of loss-free collection during outages.

### Production storage and identity

Before deployment, provision a dedicated private S3 bucket and export:

```sh
export K8S_LOKI_BUCKET=foc-production-logs
export K8S_LOKI_REGION=ap-southeast-1
export K8S_LOKI_IRSA_ROLE=arn:aws:iam::123456789012:role/foc-loki
export K8S_LOKI_STORAGE_CLASS=gp3
```

The operator rejects missing or malformed values before building or deploying.
It cannot prove from a bucket name that the bucket is dedicated: operators must
verify isolation from image storage and other consumers. Never supply the image
bucket. Enable S3 public-access blocking and encryption. Do not expire objects
sooner than Loki's seven-day retention. If versioning is enabled, separately
expire noncurrent versions; Loki deletion alone does not reclaim those bytes.

Register the cluster's OIDC issuer as an AWS IAM OIDC provider. The IRSA role
trust policy must allow `sts:AssumeRoleWithWebIdentity` only for that provider,
with **StringEquals** on `<issuer-without-https>:aud = sts.amazonaws.com` and
`<issuer-without-https>:sub = system:serviceaccount:foc-observability:loki`.
Do not use wildcard subjects or grant the role to Grafana or Alloy. The operator
adds `eks.amazonaws.com/role-arn` to Helm's exact `loki` service account. Verify
the EKS IRSA admission webhook projects a token and AWS environment settings.

Grant the role `s3:ListBucket` and `s3:GetBucketLocation` on
`arn:aws:s3:::<bucket>`, and `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`
on `arn:aws:s3:::<bucket>/*`. Customer-managed KMS encryption also requires
`kms:Encrypt`, `kms:Decrypt` and `kms:GenerateDataKey` with the appropriate key
policy. Verify regional S3 and STS reachability from the mesh and VPC endpoints.
Production uses HTTPS AWS endpoints and the SDK credential chain, without local
Floci endpoints or static test keys.

Install and authorize the EBS CSI driver before deployment. Provide the named
StorageClass, preferably encrypted gp3 with `WaitForFirstConsumer`, and a
working default StorageClass for Grafana and Alloy (or patch their PVCs in the
production overlay before first creation). RWO volumes bind instances to their
availability zone. Use a `Retain` reclaim policy and backups appropriate to the
production recovery plan; claim retention does not protect against namespace,
cluster or underlying volume deletion.

Create operator-owned Secret `grafana-admin` in `foc-observability` with nonempty
`username` and `password` keys before production deployment. The operator checks
it and never generates or overwrites production credentials. Existing production
hostname, registry, TLS, SMTP and database prerequisites still apply.

### Logging validation and acceptance

```sh
make k8s-test                         # operator tests and both Kustomize/Helm renders
.tools/bin/k8s render observability   # Kustomize plus Helm YAML for inspection only
make k8s-logging-validate             # Helm lint/template and pinned image validators
# Repeat validation/render with K8S_OVERLAY=infra/k8s/overlays/production
# and the four required production settings above.
make k8s-logging-smoke                # disruptive; managed local kind only
make k8s-smoke
make k8s-storage-smoke
# In the same isolated multi-node environment, live admission/credential/PVC tests:
K8S_RUNTIME_TESTS=1 GOWORK=off go -C scripts test -mod=readonly ./k8s \
  -run '^TestLogging(Admission|CredentialsAndPVCRetention|ReplicasRestore)LiveCluster$' -count=1
```

For isolated multi-node acceptance, set a new `K8S_CLUSTER`, private
`K8S_STATE_DIR`, and `K8S_KIND_CONFIG=infra/k8s/observability/kind.yaml` before
`make k8s-up`. CI uses this three-node topology on amd64 and arm64. Existing
clusters are reused, so a topology setting does not resize an existing cluster.
The logging smoke check uses authenticated Grafana queries, rejects an
unauthorized mesh client, checks S3 objects after flushing, restarts Loki,
Floci and Alloy, repeats historical queries, and replaces application pods to
check collection resumes. It restores application settings and waits for
readiness before reporting success. It retains credentials and PVCs. The existing mesh
check includes public Loki/log-bucket paths; the logging check exercises the
live admission chain with server-side dry runs. Image-storage checks cover
signed image access and gateway bucket restrictions.
`make k8s-status` includes observability pods, Services and claims.

Implementation validation on 7 November 2026: both environment renderings and
pinned-image Loki/Alloy validators passed. A fresh three-node kind cluster on
Linux amd64 passed bootstrap, logging collection and authenticated Grafana
queries, mesh/public-route checks, restart/replacement persistence, live
admission checks, credential preservation, PVC retention and existing signed
image-storage checks. See the [verification record](kubernetes-verification.md#november-logging-acceptance).
AWS integration and arm64 runtime acceptance remain unverified locally; CI is
configured to run both architectures. No elapsed seven-day retention test is
claimed.
