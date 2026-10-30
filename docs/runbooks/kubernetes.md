# Kubernetes development and deployment

Kubernetes is an additional deployment option. `compose.yaml`, existing
development Dockerfiles, Compose ports, source mounts and signing keys are
unchanged. Credit is deployed with its own database and Istio-restricted internal
listener; this does not claim the complete D3 business workflow.

## Start locally

Prerequisites: Docker, make, [aqua v2.65.2 or newer](https://aquaproj.github.io/docs/install/),
OpenSSL, and approximately 4 available CPUs / 8 GiB RAM. Bun 1.4.3 is installed
by aqua; Node.js is not required. Tool and image downloads require internet
access. Kubernetes images include native arm64 variants; see the short setup
guide for macOS prerequisites.

Before the first deployment, approve this repository's aqua policy once:

```sh
AQUA_ROOT_DIR="$PWD/.tools/aqua" aqua policy allow "$PWD/aqua-policy.yaml"
```

If you use a custom `AQUA_ROOT_DIR`, use it for both this command and the
`make k8s-*` commands.
Run the allow command again if `aqua-policy.yaml` changes.

To update a pinned tool, change its version in `aqua.yaml`, `.bun-version`, or
`.istio-version`, then refresh the committed checksums with
`AQUA_ROOT_DIR="$PWD/.tools/aqua" aqua update-checksum`.

```sh
# First deployment: choose the first administrator explicitly.
K8S_BOOTSTRAP_SUPERADMIN_EMAIL=admin@example.test make k8s-up
# `k8s-up` deploys, then stays attached to configured forwards; Ctrl+C closes
# the forwards while keeping the cluster running.
```

Copy `.env.example` to `.env` first. `K8S_HTTPS_PORT`, `K8S_KAFKA_UI_PORT`, and
`K8S_MAILPIT_PORT` configure local forwards; blank disables one. Defaults match
Compose's browser/Mailpit ports and Kafka UI's local port. Choose distinct host
ports to run Compose and Kubernetes simultaneously. The developer-facing
[Kubernetes setup](../../infra/k8s/README.md) has the short version.

`k8s-up` installs the pinned, checksum-verified tools into `.tools/aqua`,
creates/reuses kind cluster `foc`, installs the pinned Gateway API Standard
CRDs and Istio charts, builds/loads images, and deploys the gateway,
dependencies, database provisioning, migrations/topics and applications.
Failed Jobs stop rollout.
Existing running Jobs are not deleted. Retrying replaces only the exact terminal
initialization Jobs; databases and PVCs are not reset.

The private kubeconfig is `.local/k8s/clusters/foc.config`; your current global
Kubernetes context is not changed. Namespace `foc` holds application resources.
Run `make k8s-status` to inspect readiness and warnings. Run `make k8s-smoke`
to check HTTPS and the public User Health RPC through the gateway.

If Compose created a root-owned `.local`, follow the
[directory ownership recovery](../../LANDMINES/compose-owned-local-directory.md).
Use a consistent `K8S_STATE_DIR` override if choosing another private location.

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
`PORT`; User and Credit internal Services target `INTERNAL_PORT`, while other
internal aliases target the public listener. Istio forwards inbound traffic
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
be committed or printed into logs.

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
not undo a later role demotion. Do not delete the bootstrap row to restore
access.

Sign in through the normal magic-link flow. Mailpit receives local SMTP at
`mailpit:1025`; its web UI is port-forwarded only. Production omits Mailpit and
requires operator-provided SMTP credentials compatible with the existing sender.
Local development email is available through Mailpit.

## Initialization jobs and demo data

`make k8s-up` and a full `make k8s-deploy` prepare the database, schema, and
Kafka before rolling out applications. These Jobs do setup work; they do not
load demo users or supplier records.

| Job | Work performed |
| -- | -- |
| `db-provision` | Creates the five service databases and non-superuser roles if they are missing, grants database access, and installs Supplier's PostGIS and `pg_trgm` extensions. It does not drop existing data. |
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
target.

## Mesh boundary and application behavior

Applications do not have a `single` or `mesh` mode. `PORT` defaults to 8080 and
`INTERNAL_PORT` is configured in both Compose and Kubernetes. User exposes its
public-key RPC only on the internal listener. Credit keeps reservation RPCs on
the Order-only internal listener. Other services mount their current routes on
the public listener and keep an empty internal router as a placeholder; their
internal Service aliases currently target the public listener. Internal service
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

`-c` selects the application rather than its proxy. Production Go images have
no shell or psql. Execute an installed command, for example a local probe:

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

## Updates and deletion

```sh
make k8s-deploy service=supplier-service
# Full deploy reruns provisioning/migrations; required for schema changes:
make k8s-deploy
```

`K8S_SKIP_BUILD=1` reuses images already loaded/published. Use it only when
their source is current. `K8S_IMAGE_PREFIX` and `K8S_IMAGE_TAG` change application
and migration image references together.

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
Existing-cluster mode never creates/deletes kind or changes the global context.
Use `K8S_SKIP_BUILD=1` for already published images.

Istio initialization needs NET_ADMIN/NET_RAW. A namespace enforcing Kubernetes
Restricted Pod Security must use an appropriately configured Istio CNI setup;
the supplied, tested local profile uses Istio's standard initializer. Admission
pins that initializer/proxy shape, so an Istio upgrade must update and retest
the policy alongside the chart/version pin.

## Current-main updates

A full deployment now provisions and migrates Communication's own database,
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
and deletion stay private.

The production overlay also requires Secrets `credit-db`, `communication-db`
and `telegram` (an empty `TELEGRAM_BOT_TOKEN` disables the bot). Configure real S3
bucket/endpoints, AWS credentials and HTTPS object access in that overlay;
the local emulator and its test credentials are not included there.

For local Telegram configuration, first setup accepts
`K8S_TELEGRAM_BOT_TOKEN`. Later deployments preserve the credential file;
edit `.local/k8s/secrets/TELEGRAM_BOT_TOKEN` explicitly to rotate or clear it,
then redeploy. This matches the other preserved local credentials.

### Image sources

| Component | Upstream source | Architectures |
| -- | -- | -- |
| PostgreSQL/PostGIS | [GUI/postgis-docker](https://github.com/GUI/postgis-docker), based on official PostgreSQL | amd64, arm64 |
| Kafka / topic initialization | [Apache Kafka](https://hub.docker.com/r/apache/kafka) | amd64, arm64 |
| Kafka UI | [Kafbat](https://github.com/kafbat/kafka-ui) | amd64, arm64 |
| Mailpit | [Axllent](https://github.com/axllent/mailpit) | amd64, arm64 |
| Local S3 | [Floci](https://github.com/floci-io/floci) | amd64, arm64 |
| Istio control plane / proxies | [Istio](https://github.com/istio/istio) | amd64, arm64 |
| Go runtime | [Google Distroless](https://github.com/GoogleContainerTools/distroless) | amd64, arm64 |
| Frontend runtime | [Official Node image](https://github.com/nodejs/docker-node) | amd64, arm64 |

Application runtime images contain no Bun/compiler. The frontend runs Node;
Go services use their compiled binaries. Host tooling uses the pinned Bun
runtime. Linux amd64/arm64 and macOS hosts are supported; Docker Desktop is
required on macOS.

Access logs intentionally omit query strings using Envoy's query-free path
formatter. This keeps presigned URL signatures and login-link tokens out of
collected logs without changing requests.

Manifest application retries transient API timeouts up to three times with the
same desired resources. Permanent validation/authorization errors stop promptly.
If retries are exhausted, inspect `make k8s-status` and wait for any running
initialization Jobs before retrying. Running Jobs and persistent data are not
deleted as a timeout recovery mechanism.
