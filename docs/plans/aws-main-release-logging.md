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

## Application release gates

Logging is not the only prerequisite for releasing the selected application commit.

- B1: New reviews are inserted as `pending_moderation` in `user-service/database/query/reviews.sql`. User publishes submissions and consumes completion events, but `infra/k8s/base/applications/kustomization.yaml` and the eight-image release inventory do not deploy `content-moderation-service`. New reviews would remain pending without that worker. Add its tracked deployment and required resources before claiming this application release is complete. This is distinct from the reports/appeals `moderation-service` in PR #342. Review moderation is tracked in issue #264.
- B2: Review-photo and profile-picture storage is optional in `user-service/cmd/server/config.go`, but the AWS renderer does not configure `USER_S3_BUCKET` or a storage identity. Those image features would remain unavailable. The local-only storage initialization Job must not be deployed to AWS as a substitute.
- B3: User migration 12 drops `users.profile_picture_key`. A live TLS-verified, read-only preflight confirmed User is at migration 9 and found zero populated legacy keys; its temporary Job was removed. Recheck before migrating and assess rollback compatibility rather than assuming reverting images alone restores the old schema.

These gates do not expand the six-resource logging plan. No content-moderation or User image-storage resources are included in it.

## Release prerequisites and acceptance

- A1: After explicit approval, apply only the fingerprinted saved plan. Record its actual apply result and verify bucket encryption/public-access/HTTPS controls, exact workload trust, and bucket-scoped policy. Preserve existing email roles and unrelated resources.
- A2: Install the administrator-owned observability namespace/log-reading RBAC and scoped release RBAC. Create Grafana credentials privately only if absent. Do not relax environment approval, admission, mesh authorization, or private EKS access to make release execution succeed.
- A3: Refresh the non-secret deployment outputs, validate the release configuration, publish it to the existing versioned configuration bucket, and record its exact object version and checksum. Authorize only the existing main release workflow in the dedicated runner group. Launch a disposable runner only when the release is ready.
- A4: Deploy the pinned release, verify immutable image provenance, migrations, workload readiness, and public HTTPS. Verify encrypted logging claims, Loki's actual S3 access, a unique log marker through authenticated Grafana, and log readability after Loki pod replacement. Update this document with observed results without claiming unrun checks passed.

Logging uses the existing workers and adds 7 GiB of retained encrypted claims across Loki, Grafana, and Alloy. S3 storage/requests and those volumes add usage charges. Seven-day compactor retention requires a running compactor and does not clean up stopped deployments. Preserve bucket objects and retained disks unless their deletion is separately approved.
