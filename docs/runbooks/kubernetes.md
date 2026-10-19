# Kubernetes development and deployment

Kubernetes is an additional deployment option. `compose.yaml`, existing
development Dockerfiles, Compose ports, source mounts and signing keys are
unchanged. Credit is still a placeholder; this does not complete D3's integrated
business workflow. See [verification results](kubernetes-verification.md).

## Start locally

Prerequisites: Docker, Bash, curl, OpenSSL, tar, Node.js 24 for authentication
smoke checks, and approximately 4 available CPUs / 8 GiB RAM. Tool downloads
require internet access. The validated environment is Linux amd64; consult
[landmines](../../LANDMINES/README.md) before using PostGIS on Apple Silicon.

```sh
# First deployment: choose the first administrator explicitly.
K8S_BOOTSTRAP_SUPERADMIN_EMAIL=admin@example.test make k8s-up

# Once bootstrap has finished (do not run concurrently with deployment):
K8S_BOOTSTRAP_SUPERADMIN_EMAIL=admin@example.test make k8s-smoke

# In separate terminals:
make k8s-access
make k8s-mailpit
make k8s-kafka-ui
```

`k8s-up` installs checksum-verified tools into `.tools/k8s`, creates/reuses
kind cluster `foc`, installs pinned Istio charts from its verified release
archive, builds/loads images, and deploys four stages: dependencies, database
provisioning, migrations/topics, applications. Failed Jobs stop rollout.
Existing running Jobs are not deleted. Retrying replaces only the exact terminal
initialization Jobs; databases and PVCs are not reset.

The private kubeconfig is `.local/k8s/clusters/foc.config`; your current global
Kubernetes context is not changed. Namespace `foc` holds application resources.
Run `make k8s-status` to inspect readiness and warnings.

If Compose created a root-owned `.local`, follow the
[directory ownership recovery](../../LANDMINES/compose-owned-local-directory.md).
Use a consistent `K8S_STATE_DIR` override if choosing another private location.

## Which address belongs where?

| Caller | Address | Meaning |
| -- | -- | -- |
| Browser | `https://localhost:8443` | TLS gateway; frontend and public RPCs share one origin |
| Supplier calling User | `http://user-service-internal:8080` | Private Kubernetes DNS; Istio supplies network mTLS |
| Gateway calling User | `user-service-external:8080` | Private ClusterIP Service pointing only to public handlers |
| Developer opening Kafka UI | `http://localhost:18080` | Private operator port-forward; login required |
| Developer opening Mailpit | `http://localhost:18025` | Local/development mailbox port-forward |

Both `-external` and `-internal` are ClusterIP Services: private addresses, not
internet listeners. Both expose Service port 8080. Kubernetes maps the external
Service to the public application listener at `127.0.0.1:8080`, and the internal
Service to the combined listener at `127.0.0.1:8081`, through the destination
proxy. Names alone are not the security boundary.

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
The local gateway uses wildcard host matching because Istio expands a short
VirtualService host such as `localhost` into cluster DNS. Its local TLS
certificate has localhost SANs; production replaces both hosts with a real FQDN.

## TLS, identities and secrets

Trust `.local/k8s/secrets/tls.crt` explicitly in your development browser or OS
before using the HTTPS gateway. Bootstrap never changes your host trust store.
For curl use `--cacert .local/k8s/secrets/tls.crt`; do not disable verification.

Local credentials and signing keys remain in `.local/k8s/secrets` across retries.
Do not delete that directory while retaining PostgreSQL data. The generator does
not silently rotate database passwords or JWT keys. Credential files must never
be committed or printed into logs.

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

Provisioning creates `user_db`, `supplier_db`, and `order_db`, each with a
separate non-superuser role. The operator Job installs Supplier's PostGIS
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

Sign in through the normal magic-link flow. Mailpit receives local SMTP at
`mailpit:1025`; its web UI is port-forwarded only. Production omits Mailpit and
requires operator-provided SMTP credentials compatible with the existing sender.
No authentication test sends mail outside the local Mailpit setup.

## Mesh boundary and application behavior

`RPC_MODE=single` is the default, preserving Compose's combined router and
listener. `RPC_MODE=mesh` explicitly selects the two loopback listeners and
permits HTTP service URLs because Istio encrypts the network hop. Invalid modes
fail startup. JWT verification, roles and ownership checks remain enabled.

Internal User public keys require no user JWT, but do require mesh
authentication. They are absent from the public router (404). Protected APIs
still require a valid user JWT on either listener. Forward the user's bearer
token for operations performed on their behalf; background/key/health operations
without user context do not invent a JWT.

A native proxy and its bounded startup probe must be ready before an application
starts. Admission rejects missing/forged proxy setup, injection opt-outs, host
networking, capture overrides and additional privileged initializers. Loopback
listeners have explicit Istio ingress forwarding. Namespace-wide STRICT mTLS
and a default-deny policy apply; authenticated app-namespace workloads are
coarsely allowed, and gateway identity is explicitly denied internal port 8081.
Detailed per-service authorization and Kafka ACLs are deferred.

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
kubectl -n foc exec deployment/user-service -c user-service -- /app/server probe ready

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
# Full deploy reruns provisioning/migrations; required for schema changes:
make k8s-deploy

# Additional explicit local tests:
bash scripts/k8s/proxy-startup-smoke.sh
bash scripts/k8s/persistence-smoke.sh
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

`make k8s-demo` deliberately fails rather than claiming the absent Credit/D3
business workflow works. `K8S_CONFIRM_DELETE=foc make k8s-delete-cluster`
explicitly deletes only the managed local cluster and its PVC data. It retains
credential files. Do not run it when you need the data.

For older Compose volumes missing the Order database, use the existing
[idempotent database initialization recovery](../../LANDMINES/compose-existing-databases.md);
never use `docker compose down -v` as a workaround.

## Future deployments

`infra/k8s/base` contains portable workloads/security; local and production
overlays customize them. `overlays/production` is a template, not a
production-ready environment. Supply a real hostname, registry/tag, gateway TLS
Secret `foc-public-tls`, all application/database/UI Secrets, real SMTP, storage
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
