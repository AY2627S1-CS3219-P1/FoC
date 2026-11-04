# Additive Kubernetes deployment with Istio workload authentication

Status: accepted for the implemented deployment foundation.

## Decision

Keep Docker Compose and application routes independent of the Kubernetes
deployment. Kubernetes DNS owns service discovery; Istio owns workload
certificates, rotation and network mTLS. Application code retains JWT, role and
ownership checks, without managing mesh certificates or discovery.

Each application owns its routes. Every service runs the listener split:
`PORT` (default 8080) serves public routes and `INTERNAL_PORT` (default 8081)
serves private routes; both are mandatory, so application behavior is
identical in Compose and Kubernetes. A service without private routes yet
still binds its internal listener and serves the public handler there — a
later route split becomes a handler move rather than new plumbing. The
application does not select a deployment mode. User serves its public-key RPC
only on the internal listener; without the split, key retrieval would share
the public listener, which is no longer supported. Credit keeps reservation
RPCs on the Order-only internal listener, because those handlers do not
verify an application credential. Other services currently expose every route
publicly on both listeners; they copy the User Service pattern when they gain
private routes.

The public ClusterIP Services expose port 8080 and target `PORT`. Every
service also has an internal Service exposing port 8080 and targeting
`INTERNAL_PORT`. Backend callers use one internal base URL per destination. Ingress uses Kubernetes
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

- Compose keeps its commands, host ports, source mounts, volumes and keys.
  The User listener split intentionally changes the internal public-key address
  from `http://user-service:8080` to `http://user-service:8081`. Existing `.env`
  files must update `USER_SERVICE_BASE_URL`; see the
  [Compose upgrade instructions](../../README.md#upgrading-an-existing-compose-setup).
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
- Single PostgreSQL/Kafka instances exist only in the local overlay. The
  applications and the shared migration image are topology-agnostic: every
  consumer receives a `DATABASE_URL` (a pointer) and a `-dir` of its own
  migrations, so one bundled image does not imply one shared database.
  Local's idempotent `db-provision` Job creates roles/databases on the
  ephemeral shared instance; production pre-provisions its database (managed
  RDS or equivalent) before deploying, the operator supplies the `*-db`
  Secrets, and no provisioning Job runs. Single instances are not HA or backup
  guarantees. Kafka ACLs, other detailed service permissions, network
  egress restrictions and production operations remain separate work.
- The production overlay is a template. Credit is deployed, but the complete
  asynchronous D3 business workflow is not claimed by this infrastructure
  change.

See the [runbook](../runbooks/kubernetes.md) and
[verification record](../runbooks/kubernetes-verification.md).

## Current-main deployment and developer portability

The deployment includes Communication's optional Telegram linking API,
Order's Kafka outbox workers and image storage, and Credit's ledger/reservation
backend. Browser RPC origins all use the TLS gateway. The operator's `demo`
command remains disabled until the integrated D3 business workflow is verified.

Order's Kubernetes image includes the native proof-image decoder and the same
restricted ImageMagick policy as its existing deployment. Other Go services
remain distroless. The decoder is built from checksum-pinned upstream source:
the tested Debian ImageMagick backport rejects the policy's private temporary
spool paths, even with the later path allowance. The image build executes
the existing five-format capability check as the runtime user. Order also
retains Compose's 3 GiB/two-CPU container budget for native decoding.

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
registry supplies kind, kubectl, and Helm. A local registry entry retains
the complete Istio distribution, whose archive also supplies the Helm charts from which the control plane is installed: the
`istio-base` and `istiod` releases, configured by
`platform/istiod-values.yaml`. Chart validation checks the base and istiod
charts
that the install actually uses. TLS generation uses an OpenSSL configuration
file for compatibility with macOS's system implementation.

Host deployment orchestration is a Go CLI in the separate `scripts` module.
It reuses the repository's Go toolchain and keeps operator-only dependencies
out of service runtime modules. Make builds `.tools/bin/k8s`; the existing
`k8s-*` targets invoke that executable and the CLI loads root `.env` without
overriding exported values.

Envoy access logs use the
[query-free path formatter](https://www.envoyproxy.io/docs/envoy/v1.36.0/configuration/observability/access_log/usage)
`%PATH(NQ:PATH)%`. Login tokens and presigned S3 query credentials must not enter
access logs. Requests keep their original path/query for signature validation.
