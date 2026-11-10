# AWS release operations

Infrastructure publication and application release are separate entry points. Both run from a clean committed checkout. Neither installs the platform, creates credentials, provisions service databases, or grants access. Generated configuration stays in this checkout's ignored `.local` directory.

## Configuration contract

Schema version `2` contains `environment`, `infrastructure_commit`, `terraform_plan_sha256`, `rds_ca_sha256`, and a `deployment` object with exactly these fields:

```text
region, cluster_name, vpc_id, public_hostname, public_origin,
gateway_target_arn, image_prefix, order_bucket, order_role_arn,
ingress_role_arn, ses_sender_email, communication_email_role_arn, loki_bucket, loki_role_arn
```

The two SES fields are either both null or both populated. Public origin must match the HTTPS hostname. ECR, gateway, and workload-role identifiers must belong to the selected account/region. The database administrator, Secrets Manager reference, database connection credentials, and unrecognized Terraform outputs are never exported. Existing `*-db` Secrets own database connection configuration; reconcile those separately if infrastructure changes their endpoints or credentials.

Documents use content-addressed keys `<prefix>/config/<environment>/<sha256>.json`. The publication receipt provides the exact S3 `VersionId` and SHA-256 used by a release. Never substitute an unpinned latest object. Unknown schema versions/fields, altered bytes, wrong environment/account/region, or a changed CA bundle fail before deployment.

Version 2 requires the dedicated Loki bucket and role from the infrastructure
outputs. Version 1 documents are rejected; publish a new reviewed configuration
after the logging infrastructure change. Never edit an existing published object
or fill missing settings from inherited shell variables or Terraform state.
The release derives all four `K8S_LOKI_*` settings from this pinned document and
the renderer-owned `foc-gp3` storage class.

## Administrator setup

These are explicit prerequisites, not resources created by the wrappers. The separate [CI prerequisites](../../infra/aws/ci/README.md) Terraform root provisions the configuration bucket, scoped identities, EKS access entry, and private disposable runner launch template without starting an instance. It uses its own backend key; GitHub protection and Kubernetes RBAC are separate administrator operations:

- A dedicated private S3 bucket in the approved account, with versioning enabled, all four public-access blocks, HTTPS-only access, and encryption. The publisher writes AES256-encrypted objects. Do not grant application CI access to a Terraform-state bucket or prefix.
- An infrastructure operator with the existing Terraform backend/provider permissions and publication-prefix access. Publication also reads bucket versioning/public-access settings and existing objects for idempotent upload recovery.
- A separate deployment IAM role. Grant `eks:DescribeCluster` for the target cluster, ECR authorization plus describe/push operations for the nine application repositories, `s3:GetObjectVersion` on the configuration prefix, `s3:GetBucketVersioning` on the configuration bucket, and `s3:PutObject`/`s3:DeleteObjectVersion` only on `<prefix>/locks/*`. ECR authorization requires `ecr:GetAuthorizationToken` on `*`; do not broaden other permissions. No Terraform backend, Secrets Manager, IAM administration, or infrastructure provisioning permissions are needed.
- EKS identity mapping and Kubernetes RBAC for the existing operator. It reconciles application resources in `foc`, the named ingress/binding in `istio-ingress`, logging workloads in `foc-observability`, and the existing named StorageClass. Namespaces, admission, CRDs and RBAC remain administrator-owned. The logging Role permits Helm's namespace-local release-history Secrets as well as the existing Grafana credential Secret; this is trusted deployment access. Review the rendered operator resource set rather than granting unrelated AWS administrator privileges.
- Existing Istio, Gateway API, AWS Load Balancer Controller, encrypted storage support, database schemas/extensions, and ten application workload Secrets (including `content-moderation-db` and `moderation-llm`). Install `infra/k8s/base/observability-rbac` with an administrator and supply `foc-observability/grafana-admin` (`username`/`password`) privately, following the AWS runbook. SMTP deployments additionally need `smtp`; SES deployments do not.
- An approved network path from the runner to the restricted EKS endpoint. Do not open the endpoint to the internet. AWS CLI v2, Docker with linux/amd64 build capability, Python 3.9+, Make, Go, and the pinned aqua tools must be available.

For GitHub, use a trusted **ephemeral** Linux x64 runner with label `foc-deploy` in organization group `foc-release`. Restrict the group to this repository and exactly `AY2627S1-CS3219-P1/FoC/.github/workflows/aws-release.yml@refs/heads/main`; until that file exists on `main`, keep its workflow allowlist empty. Labels alone do not prevent a public-repository PR from claiming a runner. Do not execute untrusted PR workloads on that runner. Configure the protected `aws-demo` environment with required reviewers and permitted deployment branches before using the workflow. Configure the deployment role's GitHub OIDC trust for this repository and that environment (`aud=sts.amazonaws.com`, exact environment subject); restrict allowed branches in the environment. Set environment variables `AWS_ACCOUNT_ID`, `AWS_REGION`, `AWS_RELEASE_ROLE_ARN`, `AWS_CONFIG_BUCKET`, and `AWS_CONFIG_PREFIX`. Do not put AWS access keys in repository secrets.

The workflow runs automatically after CI succeeds on a push to `main` and checks out the exact commit CI tested. It launches and cleans up its own runner (see the [CI prerequisites](../../infra/aws/ci/README.md#continuous-deployment)). A manual dispatch from the default branch checks out its dispatch SHA. Its configuration inputs default to the `aws-demo` variables `AWS_CONFIG_VERSION` and `AWS_CONFIG_SHA256`. It becomes available after the workflow exists on that default branch. It does not release arbitrary PR refs. Its 55-minute bound is shorter than the requested one-hour role session.

## Apply and publish

Use an approved infrastructure identity and an already initialized Terraform backend. Review the saved plan independently; the wrapper never generates or auto-approves a new plan.

```sh
umask 077
mkdir -p .local/aws/publications
# Download over trusted HTTPS, then retain this reviewed CA bundle for publication.
curl --fail --silent --show-error \
  https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem \
  -o .local/aws/publications/global-bundle.pem

# REVIEWED_PLAN is an existing saved plan inside .local, readable only by you.
PLAN_SHA=$(python3 -c 'import hashlib,os; print(hashlib.sha256(open(os.environ["REVIEWED_PLAN"],"rb").read()).hexdigest())')
python3 scripts/aws/apply_and_publish.py \
  --account-id "$AWS_ACCOUNT_ID" --region "$AWS_REGION" \
  --environment aws-demo --bucket "$AWS_CONFIG_BUCKET" --prefix "$AWS_CONFIG_PREFIX" \
  --plan "$REVIEWED_PLAN" --plan-sha256 "$PLAN_SHA" \
  --rds-ca .local/aws/publications/global-bundle.pem \
  --document ".local/aws/publications/$PLAN_SHA.json"
```

Export `REVIEWED_PLAN` before the command above and compare `PLAN_SHA` with the fingerprint of the plan actually reviewed. Supplying a freshly computed digest does not itself review the plan. The wrapper snapshots the exact reviewed bytes, checks caller account and private/versioned storage, applies that snapshot, and prints only phase/provenance information. Provider output stays in private diagnostics, not CI logs.

If upload fails after `EXPORT` is printed, retain the file and retry **only publication** using the printed checksum:

```sh
python3 scripts/aws/apply_and_publish.py --publish-only \
  --account-id "$AWS_ACCOUNT_ID" --region "$AWS_REGION" \
  --environment aws-demo --bucket "$AWS_CONFIG_BUCKET" --prefix "$AWS_CONFIG_PREFIX" \
  --document "$EXPORTED_DOCUMENT" --config-sha256 "$EXPORTED_SHA256"
```

Publish-only never invokes Terraform and does not alter the document. Repeating an already successful publication reuses the existing identical object/version. Never retry a failed apply automatically: inspect partial state and review a fresh plan. If apply succeeded but export failed, the local `.applied.json` marker prevents repeating that apply; recover the export under infrastructure-operator review before publication. Do not edit a published document or delete its version to make a release proceed.

## Release

Use the already-assumed, narrowly scoped deployment identity. The wrapper verifies its role ARN/account rather than assuming an infrastructure role. For CI, dispatch **AWS release** with `config_version` and `config_sha256` from the publication receipt. The workflow delegates all release behavior to this command:

```sh
python3 scripts/aws/deploy_release.py \
  --account-id "$AWS_ACCOUNT_ID" --region "$AWS_REGION" \
  --role-arn "$AWS_RELEASE_ROLE_ARN" --environment aws-demo \
  --bucket "$AWS_CONFIG_BUCKET" --prefix "$AWS_CONFIG_PREFIX" \
  --config-version "$CONFIG_VERSION" --config-sha256 "$CONFIG_SHA256" \
  --release-id "$RELEASE_ID"
```

Use a new lowercase release ID (1–63 letters/digits/hyphens) for every attempt. A release checkout must not contain a root `.env`; AWS authentication comes from its approved session/profile. The wrapper creates an isolated kubeconfig and Docker login, verifies existing application/Grafana Secrets, CRDs and immutable ECR repositories, and forces the full `make k8s-deploy service=` path with linux/amd64 images tagged `<source-commit>-<release-id>`. It rejects any existing tag instead of overwriting or resuming partial pushes. The operator owns dependency and observability rollout, initialization/migration completion, application rollout, and its existing running/terminal Job handling.

The CA is retrieved from the fixed AWS HTTPS trust-store URL and checked against the publication's digest. An optional `--rds-ca <file>` supplies exactly the same pinned bytes for an offline/pre-fetched bundle. Overlays are always regenerated with the current source's renderer. `make k8s-public-smoke` checks trusted public HTTPS frontend/health and private-route exclusion without local forwards or mesh-mutating probes. A private release receipt records source/configuration provenance, nine ECR digests, and smoke success. This is not proof of login, email, uploads, or business-workflow acceptance.

## Serialization and recovery

GitHub queues releases for the environment without cancelling a running release. A conditional S3 lock at `<prefix>/locks/<environment>.json` also serializes direct wrapper invocations. Lock acquisition never steals or expires an existing lock. Normal completion/failure deletes exactly the lock version created by that attempt, not a successor's version. Docker credentials are removed on normal completion/failure. An interrupt retains the lock and private runtime files because deployment descendants might still be active; it never unlocks on an uncertain termination.

An interrupted/killed process or timed-out runner can leave a lock and private Docker credentials. An administrator must inspect its owner (`release_id`, source commit, config checksum), confirm the originating workflow/process has stopped, and inspect any still-running migration Jobs before deleting that **exact lock version** and removing the private Docker runtime directory (or destroying the ephemeral runner). Do not use a blind delete or automatic timeout takeover. A failed migration or smoke check cannot produce a successful release result; no automatic application/schema rollback is attempted. Retry after correcting the cause with a fresh release ID. Do not apply incompatible infrastructure while a release is running.

No generated configuration, `.local`, Terraform plan/state, kubeconfig, credentials, or Secret data is uploaded or cached by the workflow. Destroy the ephemeral runner after each attempt. Private diagnostics can contain credentials; inspect them locally and share only redacted phase/results. Application CI must never fetch Terraform state to recover missing configuration.

## Offline verification

```sh
PYTHONDONTWRITEBYTECODE=1 .tools/aws-python/bin/python -m unittest discover -s scripts/aws -p 'test_*.py' -v
GOWORK=off go -C scripts test -mod=readonly -race ./...
make fmt-check
make lint
```

The CLI fixture executes real publication/release entry points and the actual overlay renderer, with explicit fake AWS/Docker/Git/Make/cluster commands. It injects failed upload/recovery, changed latest object, identity/CA/checksum/environment failures, non-immutable repositories, missing Secrets, lock contention, an interrupted wrapper with a still-running descendant, migration failure, and smoke failure. Actual kubectl renders the generated manifests. Existing Go tests plus public-smoke command fixtures cover the operator seams. These checks do not apply infrastructure, contact a live cluster, build real images, or send messages.
