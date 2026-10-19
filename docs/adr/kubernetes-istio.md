# Additive Kubernetes deployment with Istio workload authentication

Status: accepted for the implemented deployment foundation.

## Decision

Keep Docker Compose unchanged and add independent production images, Kustomize
workloads/overlays, and a kind/Helm bootstrap. Kubernetes DNS owns service
discovery; Istio owns workload certificates, rotation and network mTLS.
Application code retains JWT, role and ownership checks, without managing mesh
certificates or discovery.

Go defaults to the existing single combined router. Explicit mesh mode binds
public and internal routers to loopback ports 8080 and 8081. Public generated
handlers are registered once; the internal router delegates to them as a
fallback. Internal-only generated services are not mounted publicly. This avoids
a per-procedure ingress allowlist or separate public/private client adapters.

Both private ClusterIP Services expose port 8080:
`<service>-external` maps to workload port 8080 and
`<service>-internal` maps to workload port 8081. Backend callers use one internal
base URL per destination. Gateway routing is by protobuf namespace, never a
duplicate list of individual methods. Gateway identity is explicitly denied
internal workload port 8081.

Mandatory native proxies, bounded proxy startup probes, loopback bindings,
explicit Istio ingress forwarding, fail-closed admission, STRICT mTLS and
coarse namespace authentication prevent ordinary network bypass. Application
workloads in the FoC namespace are currently trusted to call each other;
fine-grained service authorization is deferred. User JWTs remain required only
for user-context operations. Public-key fetching needs mesh authentication but
no user JWT.

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
  or backup guarantees. Kafka ACLs, detailed service permissions, network egress
  restrictions and production operations remain separate work.
- The production overlay is a template. Credit and the complete asynchronous
  D3 business workflow are not implemented by this infrastructure change.

See the [runbook](../runbooks/kubernetes.md) and
[verification record](../runbooks/kubernetes-verification.md).
