# AWS release logging prerequisite

## Status and source

The saved logging-only Terraform plan proposes **6 additions, 0 updates, and 0 deletions**. It has not been approved or applied. No full application release has run.

- Application release selected: `6cd9843fe261b2ce9b54583865f99d5d5c141dd2`. Its GitHub CI passed.
- Infrastructure source: `59dc3a47c17c6c65f095f1684b3dacfb65a8e248`, containing the additive email ownership declarations in [PR #341](https://github.com/AY2627S1-CS3219-P1/FoC/pull/341).
- Saved binary plan SHA-256: `e4204ea329f3dd89099e994f4136292e4b29313320b6c4c3801ce32fbce0eeb3`.

The logging implementation is already tracked in `infra/aws/observability.tf`, `infra/k8s/base/observability`, `infra/k8s/base/observability-rbac`, and the deployment operator. This document records the bounded infrastructure plan required before its first AWS release. The ownership and retention contract is defined in [the logging ADR](../adr/0001-kubernetes-logging.md).

## Exact infrastructure scope

| Terraform address | Action | Purpose |
| --- | --- | --- |
| `aws_s3_bucket.loki` | Create | Dedicated log storage, separate from Order uploads and Terraform state |
| `aws_s3_bucket_public_access_block.loki` | Create | Block public ACLs and bucket policies |
| `aws_s3_bucket_server_side_encryption_configuration.loki` | Create | AES256 encryption at rest |
| `aws_s3_bucket_policy.loki` | Create | Deny requests without HTTPS |
| `aws_iam_role.loki` | Create | Trust only the cluster's `foc-observability:loki` ServiceAccount and STS audience |
| `aws_iam_role_policy.loki` | Create | List this bucket and read, write, and delete only its objects |

The IAM policy's concrete bucket ARN is computed when AWS creates the bucket. The tracked expression references only `aws_s3_bucket.loki`; the compactor needs object deletion for retention.

## Boundaries

- D1: Use the tracked additive IAM configuration, not the older email ownership mapping on the selected application commit. Applying that older mapping could replace User's role and remove Communication's separately tracked role.
- D2: Target only the six logging resources for this prerequisite. The broader infrastructure plan contains unrelated EKS changes. Node groups, add-ons, databases, email roles, existing buckets, ingress, and moderation infrastructure are excluded from this apply. Targeting is an exceptional bounded operation, not a claim that the full stack has no drift.
- D3: Keep binary plans, Terraform state, provider JSON, private variables, credentials, and execution logs outside Git. Verify the saved binary checksum immediately before applying. Regenerating or changing a plan requires updating its reviewed scope and fingerprint here before use.

Reproduce the scope from the infrastructure source above using its existing protected S3 backend and private deployment inputs:

```sh
terraform -chdir=infra/aws plan -input=false -lock-timeout=60s \
  -var-file="$PRIVATE_TFVARS" -out="$PRIVATE_PLAN" \
  -target=aws_s3_bucket.loki \
  -target=aws_s3_bucket_public_access_block.loki \
  -target=aws_s3_bucket_server_side_encryption_configuration.loki \
  -target=aws_s3_bucket_policy.loki \
  -target=aws_iam_role.loki \
  -target=aws_iam_role_policy.loki
```

This command creates a new plan, not the fingerprinted binary above. Inspect its JSON for the exact six create actions and no other managed-resource changes before requesting approval.

## Release prerequisites and acceptance

- A1: After explicit approval, apply only the fingerprinted saved plan. Record its actual apply result and verify bucket encryption/public-access/HTTPS controls, exact workload trust, and bucket-scoped policy. Preserve existing email roles and unrelated resources.
- A2: Install the administrator-owned observability namespace/log-reading RBAC and scoped release RBAC. Create Grafana credentials privately only if absent. Do not relax environment approval, admission, mesh authorization, or private EKS access to make release execution succeed.
- A3: Refresh the non-secret deployment outputs, validate the release configuration, publish it to the existing versioned configuration bucket, and record its exact object version and checksum. Authorize only the existing main release workflow in the dedicated runner group. Launch a disposable runner only when the release is ready.
- A4: Deploy the pinned release, verify immutable image provenance, migrations, workload readiness, and public HTTPS. Verify encrypted logging claims, Loki's actual S3 access, a unique log marker through authenticated Grafana, and log readability after Loki pod replacement. Update this document with observed results without claiming unrun checks passed.

Logging uses the existing workers and adds 7 GiB of retained encrypted claims across Loki, Grafana, and Alloy. S3 storage/requests and those volumes add usage charges. Seven-day compactor retention requires a running compactor and does not clean up stopped deployments. Preserve bucket objects and retained disks unless their deletion is separately approved.
