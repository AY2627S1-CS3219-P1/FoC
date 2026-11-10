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

## AWS demo integration

The AWS Terraform stack owns a dedicated private encrypted log bucket and a role
whose web-identity trust names only `foc-observability:loki` and the STS audience.
The role can list that bucket and read, write and delete its objects; deletion
is needed by the compactor. Order uploads, Terraform state and their identities
remain separate. No static AWS credentials are supplied to Loki.

The generated AWS overlay includes Grafana/Alloy and mesh policies, with encrypted
retained `foc-gp3` claims. The operator supplies Loki's bucket, region, role and
storage class to the existing pinned Helm chart. The platform administrator
installs `base/observability-rbac` separately and supplies the private Grafana
admin Secret, so application releases do not gain RBAC administration.

All three logging disks and S3 objects require explicit teardown decisions.
Seven-day retention requires a running compactor and does not clean up a stopped
deployment. See the [AWS runbook](../../infra/aws/README.md) for setup and costing.
