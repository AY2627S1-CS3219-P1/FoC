# Publish configuration independently of application releases

Application releases consume a versioned, non-secret configuration document from private S3, not Terraform state or stored Kubernetes overlays. Each release pins the object version and checksum, obtains the checksum-pinned RDS CA bundle, and regenerates overlays with the repository renderer. This separates infrastructure privileges from application deployment and keeps generated configuration aligned with the application source being released.

The publication boundary exports an explicit allowlist of renderer/deployer inputs. Database credentials, database-administrator identifiers, Secrets Manager identifiers, kubeconfig, and Terraform plans/state are excluded. Existing workload Secrets and platform installation remain infrastructure-operator prerequisites, not application-CI responsibilities.

A reviewed Terraform plan is applied once and its exported document is retained privately before upload. Upload retries reuse that exact document without Terraform. Releases use fresh immutable source/attempt image tags and a conditional versioned S3 environment lock; a failed attempt is not automatically resumed or rolled back. The existing Kubernetes operator retains ownership of build, deployment order, migration waits, and Job safety. A separate read-only public HTTPS smoke command shares its health and route assertions with local mesh verification without running cluster-mutating mesh probes.

See [AWS release operations](../runbooks/aws-releases.md) for configuration fields, permissions, recovery, and the protected runner boundary.
