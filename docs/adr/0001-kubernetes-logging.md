# Single-instance Kubernetes logging with a Helm-owned Loki

Deploy the maintained `grafana-community/loki` chart 21.0.0 (Loki 3.7.9)
with one monolithic instance. Helm owns Loki configuration, service account,
Services and StatefulSet; Kustomize owns namespace, mesh access, Grafana, Alloy,
RBAC and local bucket provisioning. Keeping those ownership boundaries avoids
Helm/Kustomize reconciliation conflicts while retaining the chart's maintained
Loki configuration and upgrade path.

A single Alloy collects unchanged pod log lines through the Kubernetes API in
`foc` and `istio-ingress`, including native Envoy sidecars. Only infrastructure
metadata becomes stream labels. This avoids privileged per-node mounts, at the
cost of API-server traffic and gaps or duplicates during collector outages and
pod replacement. Alloy's persistent storage does not imply guaranteed delivery;
its experimental log-write WAL is deliberately not enabled.

Loki uses TSDB v13 with seven-day compactor retention, signed S3 requests and a
dedicated bucket: Floci locally, AWS S3 via IRSA in production. Mesh STRICT mTLS
and service-account authorization protect the private, single-tenant API.
Grafana requires login and is available through operator port-forwarding.

This design accepts a single-instance failure domain, retained PVC management,
and shared local Floci disk capacity in exchange for a small operational footprint.
It does not promise high availability or loss-free collection. Retention is
configuration-validated; no seven-day elapsed test is claimed.
