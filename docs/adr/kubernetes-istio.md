# Additive Kubernetes deployment with Istio workload authentication

Status: accepted for the implemented deployment foundation.

## Decision

Keep Docker Compose and application routes independent of the Kubernetes
deployment. Kubernetes DNS owns service discovery; Istio owns workload
certificates, rotation and network mTLS. Application code retains JWT, role and
ownership checks, without managing mesh certificates or discovery.

Each application owns its routes. `PORT` defaults to 8080. A service with
private routes also sets `INTERNAL_PORT`, which starts a second listener; when
`INTERNAL_PORT` is unset, every route shares the public listener, which keeps
single-port local development simple. The application does not select a
deployment mode. When `INTERNAL_PORT` is set, User serves its public-key RPC
only on the internal listener; without it, the single shared listener also
serves key retrieval (the local single-port mode). Credit keeps reservation
RPCs on the Order-only internal listener regardless, because those
handlers do not verify an application credential. Other services currently
expose every route publicly and run a single listener; they copy the User
Service pattern when they gain private routes.

The public ClusterIP Services expose port 8080 and target `PORT`. User and
Credit internal Services expose port 8080 and target `INTERNAL_PORT`. Backend
callers use one internal base URL per destination. Ingress uses Kubernetes
Gateway API
resources. Since standard `PathPrefix` matches complete slash-separated path
elements, handwritten `HTTPRoute` resources match full public protobuf service
paths (for example `/user.v1.AuthService`). Add a route for each new public
protobuf service; methods within an existing service need no route change.
User's internal-only public-key service is omitted from Gateway routes. Gateway
identity is explicitly denied internal workload port 8081.

Mandatory native proxies, bounded proxy startup probes, explicit Istio ingress
forwarding, fail-closed admission, STRICT mTLS and coarse namespace
authentication prevent ordinary network bypass. Application workloads in the
FoC namespace are broadly trusted, with one narrow exception: Credit's
reservation listener accepts only the Order workload principal. User JWTs
remain required for user-context operations. User public-key fetching needs
mesh authentication but no user JWT.

## Consequences

- Compose commands, dev files, routes, ports, volumes and keys remain compatible.
- Backend callers do not map methods to ports. Raw pod-IP calls are unsupported;
  use Service DNS so Istio can choose the authenticated upstream.
- Proxy readiness gates startup. Runtime proxy failure makes the pod unready
  and restarts the proxy, but does not automatically terminate the application.
- Kubernetes administrators and same-pod debug containers are trusted.
  Operator exec/port-forward paths are not proof of mesh isolation.
- Initialization uses completing native-sidecar Jobs. Database and Kafka PVCs
  survive pod replacement. Bootstrap credentials/keys are preserved separately.
- Frontend adapter-node preserves the SSR shell plus browser-driven data flow.
- Kafka UI uses separate operator login and private port-forward access.
  Mailpit is local only; production must provide SMTP.
- Single PostgreSQL/Kafka instances simplify local verification and are not HA
  or backup guarantees. Kafka ACLs, other detailed service permissions, network
  egress restrictions and production operations remain separate work.
- The production overlay is a template. Credit is deployed, but the complete
  asynchronous D3 business workflow is not claimed by this infrastructure
  change.

See the [runbook](../runbooks/kubernetes.md).

## Current-main deployment and developer portability

The deployment includes Communication's optional Telegram linking API,
Order's Kafka outbox workers and image storage, and Credit's ledger/reservation
backend. Browser RPC origins all use the TLS gateway. The operator's `demo`
command remains disabled until the integrated D3 business workflow is verified.

Local object storage uses Floci 2.3.0 with S3 signature enforcement and a PVC.
Its loopback listener is forwarded by Envoy. The gateway permits only object
GET/HEAD/PUT/OPTIONS beneath `/order-images/`; bucket administration remains
internal. Presigned URLs use the browser HTTPS origin with their original Host
and path preserved. The existing Order initializer provisions the bucket in a
completing local-only Job. Production must supply real S3 configuration and
credentials through its environment-specific overlay.

Kubernetes PostgreSQL and the provisioning Job use the multiarch
`nickblah/postgis:18.6-trixie-postgis-3.6.4` image from
[GUI/postgis-docker](https://github.com/GUI/postgis-docker), pinned to OCI index
`sha256:48edec2374758f62a5336ee0279aeca0107c1c63536f0a0872bee2a7b3126237`.
The upstream Dockerfile extends official PostgreSQL with packaged PostGIS.
It preserves PostgreSQL 18.6, PostGIS 3.6.4, UID 999 and the existing data path,
while adding native arm64 support. This dependency image is independent of
Compose's image selection and persistence.

Kubernetes CLI tools are pinned in the root aqua configuration and installed
under `.tools/aqua`; checksum verification is required. Aqua's standard
registry supplies kind, kubectl, and Helm. Small local registry entries retain
Bun's baseline Intel binary and the complete Istio distribution, whose archive
also supplies the Helm charts from which the control plane is installed: the
`istio-base` and `istiod` releases, configured by
`platform/istiod-values.yaml`. Chart validation checks the base and istiod
charts
that the install actually uses. TLS generation uses an OpenSSL configuration
file for compatibility with macOS's system implementation.

Envoy access logs use the
[query-free path formatter](https://www.envoyproxy.io/docs/envoy/v1.36.0/configuration/observability/access_log/usage)
`%PATH(NQ:PATH)%`. Login tokens and presigned S3 query credentials must not enter
access logs. Requests keep their original path/query for signature validation.
