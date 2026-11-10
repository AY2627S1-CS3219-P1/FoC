# Provision review moderation

Use `main` with the full moderation stack merged. CMS assesses text; User Service
owns publication and ratings. Settings take effect at startup.

## AWS deployment

Start with the [AWS platform setup](../../infra/aws/README.md#1-private-inputs-and-state-after-approval)
and [release administrator setup](aws-releases.md#administrator-setup) completed:
EKS/RDS, cluster platform, operator kubeconfig and the two Terraform backends.
Run the steps below from the repository root with the infrastructure/operator
identity. The restricted release identity cannot provision databases or CMS Secrets.

### 1. Apply the updated infrastructure

Review plans for **both** `infra/aws` and `infra/aws/ci` using their existing
backends. Apply through the [infrastructure apply/publication procedure](aws-releases.md#apply-and-publish)
and [CI provisioning procedure](../../infra/aws/ci/README.md). Retain the
publication's `CONFIG_VERSION`, `CONFIG_SHA256` and RDS CA file for the release.

Expect a `content-moderation-service` ECR repository and scoped release-role
access: nine images in total. CMS shares the existing EKS/RDS/network resources.
The operator supplies workload Secrets; Terraform does not hold the provider key.

Check `CMS_LLM_MODEL_ID` and `CMS_LLM_BASE_URL` in
[`foc-config`](../../infra/k8s/base/dependencies/config.yaml) before releasing.
Use a model actually served by the provider. Merge non-secret setting changes to
main before dispatching a release; never put the key there.

### 2. Render the operator overlay

Use the publication's CA file and the operator's `KUBECONFIG` / `K8S_CONTEXT`:

```sh
set -euo pipefail
umask 077
mkdir -p .local/aws
terraform -chdir=infra/aws output -json deployment > .local/aws/deployment.json
make aws-tools k8s-tools
export PATH="$PWD/.tools/aqua/bin:$PATH"
export RDS_CA_FILE="$PWD/.local/aws/publications/global-bundle.pem"
.tools/bin/aws render-overlay --outputs .local/aws/deployment.json \
  --rds-ca "$RDS_CA_FILE" --output .local/aws/production
k() { kubectl --context "$K8S_CONTEXT" "$@"; }
k apply -k .local/aws/production/dependencies
```

The overlay sets the public CMS URL and mounts the RDS CA in CMS and its migration Job.

### 3. Provision the service database and database Secret

Use the **same retained** `.local/aws/db` directory on retries. The preparer
preserves existing credentials and adds `foc_content_moderation` / `content_moderation_db`, with
a `content-moderation-db` Secret containing its TLS-verified `DATABASE_URL`.

```sh
DATABASE_SECRET_ARN=$(python3 -c 'import json; print(json.load(open(".local/aws/deployment.json"))["database_secret_arn"])')
aws secretsmanager get-secret-value --secret-id "$DATABASE_SECRET_ARN" \
  --region "$AWS_REGION" --query SecretString --output text > .local/aws/rds-admin.json
.tools/bin/aws prepare-database --outputs .local/aws/deployment.json \
  --admin-secret .local/aws/rds-admin.json --directory .local/aws/db
k apply -f .local/aws/db/secrets.json
k apply -f .local/aws/db/job.json
k -n foc wait --for=condition=complete job/rds-provision --timeout=300s
```

The Job creates six databases/roles and Supplier's extensions. On retry, keep
credential files; wait for running Jobs and delete only terminal Jobs before
recreating them. After success, remove temporary administrator access:

```sh
k -n foc delete job/rds-provision secret/rds-provision-admin serviceaccount/rds-provision
rm .local/aws/rds-admin.json .local/aws/db/secrets.json
```

Retain the six per-service credential JSON files privately; the generated Secret
list can be recreated. Do not print credentials or provisioning logs.

### 4. Install the provider Secret

The operator supplies a private, nonempty `.local/aws/CMS_LLM_API_KEY` file
containing the key without quotes or a trailing newline. Install it from the file:

```sh
test -s .local/aws/CMS_LLM_API_KEY
chmod 600 .local/aws/CMS_LLM_API_KEY
k -n foc create secret generic moderation-llm \
  --from-file=CMS_LLM_API_KEY=.local/aws/CMS_LLM_API_KEY \
  --dry-run=client -o yaml | k apply -f -
k -n foc get secret content-moderation-db moderation-llm -o name
```

Install the remaining signing/email/application Secrets using the
[AWS checklist](../../infra/aws/README.md#4-provision-the-six-databases-and-remaining-secrets).
An empty provider key leaves workers idle.

### 5. Release the application stack from main

Apply the updated [release RBAC](../../infra/aws/ci/release-rbac.yaml) with the
administrator identity, then dispatch the existing protected release workflow:

```sh
k apply -f infra/aws/ci/release-rbac.yaml
gh workflow run aws-release.yml --ref main \
  -f config_version="$CONFIG_VERSION" -f config_sha256="$CONFIG_SHA256"
```

Wait for workflow success: Secret checks, nine image builds/pushes, migrations,
Kafka topics, application rollout and public HTTPS smoke must all pass.

```sh
k -n foc wait --for=condition=complete job/content-moderation-migrate --timeout=180s
k -n foc rollout status deployment/content-moderation-service --timeout=180s
k -n foc logs deployment/content-moderation-service --tail=30
```

Expect worker-pool and outbox-relay startup logs; configuration errors stop workers.

### 6. Verify the feature, not just health

At the public origin, POST `{}` to
`/moderation/grpc.health.v1.Health/Check`; expect a serving response. Then use two
approved test accounts and a completed order:

1. Leave a benign review. Submission returns pending; public visibility and
   ratings must follow approval, never submission alone.
2. Refresh the order or open **Reviews you left → View unpublished reviews**.
   Wait for assessment. Approval must publish the review and update the rating
   count/average once. Published reviews have no status chip on the order page.
3. In `/admin/reviews/unpublished`, look up the author's email. For a review
   rejected by the policy, verify it remains unpublished, has rejection reasons,
   does not affect ratings and still blocks another review for that order.

Health and model discovery do not prove inference. Verify the actual job verdict
and publication. Local SoCLaaS approval/delivery/ratings passed on 10 November
2026 with `llama3.1:8b`; `gemma4:26b` returned 503 then. Verify the deployed model anew.

## Local Compose

Fill the required database settings and `CMS_LLM_MODEL_ID`, `CMS_LLM_BASE_URL`,
`CMS_LLM_API_KEY` in `.env` (use `.env.example` for a new file), then:

```sh
docker compose up -d --build
make seed-demo
```

Frontend: `http://localhost:5173`; login mail: `http://localhost:8025`. Repeat
step 6. For existing volumes missing CMS's database, see the
[database initialization workaround](../../LANDMINES/new-service-database.md).

## Change settings or recover stalled reviews

For a provider-key change, replace `moderation-llm` using step 4 and run
`k -n foc rollout restart deployment/content-moderation-service`, then wait for
rollout completion. For model/endpoint changes, update the reviewed ConfigMap
source and release again. All replicas must read the corrected startup settings.

| Symptom | Check / action |
| --- | --- |
| Pending with no attempts | Confirm a nonempty provider key, worker-start logs and submission-topic ingestion. |
| Configuration error; workers stopped | Correct settings and restart CMS. The attempt records `configuration`; releasing the job emits no completion event. |
| Connection, timeout, rate limit or invalid output | Inspect sanitized attempts; bounded retries run automatically. |
| Terminal assessment error | Inspect through the admin moderation RPC and retry with its current expected generation. This is not a content rejection. |
| CMS completed but User remains pending | Check completion outbox, Kafka and User consumer logs; delivery is at least once and User deduplicates it. |
| Crash / lost claim | Lease expiry recovers work. Repeated crashes/releases consume the attempt budget. |
| Consumer repeatedly fails decoding a Kafka record | Inspect the failing record and repair the source/offset deliberately; restarting alone does not acknowledge it. |

Operational logs report queue depth, oldest work, failures and pending outbox
counts (`CMS_LOGGING_INTERVAL`). Direct pod health is
`/grpc.health.v1.Health/Check`; gateway health uses the `/moderation` prefix.
Use the [service README](../../content-moderation-service/README.md) for runtime
settings/tests and the [ADR](../adr/review-content-moderation.md) for guarantees.
