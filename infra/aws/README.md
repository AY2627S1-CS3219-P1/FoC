# Ten-day AWS demo

This is a preparation package, not evidence of a running deployment. No command
below should create AWS resources, publish images, or change a cluster without
operator approval. The account's quotas and organization restrictions, regional
engine availability, actual prices, domain ownership, and SES sending status
must be checked before apply. `terraform validate` and the mocked tests do not
prove that the school permits deployment.

## Decisions log

- D1: Two On-Demand `t3.xlarge` workers in private subnets across two AZs, one NAT
  gateway, and an internal TCP NLB. API Gateway HTTP API supplies the public
  AWS-managed HTTPS hostname and forwards through a VPC Link to the internal NLB.
  Terraform owns both API Gateway and the NLB. A TargetGroupBinding registers
  Istio gateway pod IPs. The NAT is a single-AZ failure dependency and cross-AZ
  traffic can cost extra. No owned domain or user-provided TLS certificate is needed.
- D2: Single-AZ `db.t4g.small` PostgreSQL, one Kafka broker with a 5 GiB encrypted
  gp3 claim, and one application replica each. This is a demo, not high availability.
- D3: T3 standard credits avoid surplus-credit charges but can throttle sustained
  CPU. Node updates use MINIMAL strategy to avoid default surge capacity. Expect
  downtime during node updates, particularly for single-replica workloads and Kafka.
- D4: IRSA supplies short-lived AWS credentials to Order, Loki, the EBS CSI driver,
  the load-balancer controller, and optionally User and Communication for SES. Each
  has a separate workload role. Application AWS access keys are not needed.
  RDS manages the administrator password in Secrets Manager. Terraform does not
  read that password or generate SMTP/JWT/application database credentials.
- D5: State uses a separate versioned, encrypted S3 bucket with native S3 locking.
  `ExpiresOn` is a tag only. It does not delete or stop anything.
- D6: RDS deletion protection and a final snapshot, S3 `force_destroy=false`, ECR
  `force_delete=false`, and Kafka's Retain policy require deliberate data decisions
  during teardown. Retained resources continue accruing charges.
- D7: HTTPS terminates at API Gateway. VPC Link/NLB-to-Istio traffic uses private
  HTTP, restricted by security groups. Service-to-service mesh mTLS remains.
  This does not provide TLS on every network segment. Private ingress has no
  hostname constraint because the proxy may supply an integration Host header.
  Public browser origins remain exact HTTPS origins.
- D8: Logging follows the [Kubernetes logging ADR](../../docs/adr/0001-kubernetes-logging.md):
  Helm owns Loki, Kustomize owns private Grafana/Alloy workloads, and the platform
  administrator installs Alloy's log-reading RBAC. Terraform supplies a dedicated
  private Loki bucket/IRSA role. All three logging claims use encrypted retained gp3.
- B1: SMTP credentials, a verified SES sender, SES production approval, bootstrap
  email, expiry date, and approved AWS account must be supplied privately. These
  inputs do not block offline preparation. They block a complete deployment.
- B2: Quota/service restrictions and live sizing remain unverified. Eight vCPUs are
  needed for initial workers. No quota-inspection denial is permission to bypass
  organization policy. If deployment is denied, stop and ask the account owner.

## Ten-day cost estimate

Prices checked Mon 9 Nov 2026 against AWS's public Singapore catalogs. This is
an estimate for 240 continuously running hours, not a live Terraform plan or
billing forecast. Use USD On-Demand rates before credits, free-tier allowances,
organization discounts, taxes, and currency conversion. Storage uses a 30-day
month approximation; actual billing is prorated. Recheck prices before apply.

### Running infrastructure

| Resource | Quantity and rate | Ten days (USD) | Source |
| --- | --- | ---: | --- |
| Linux `t3.xlarge` workers | 2 x 240 hours x $0.2112/hour | 101.38 | P1 |
| EKS standard-support control plane | 240 hours x $0.10/hour | 24.00 | P2 |
| Single-AZ PostgreSQL `db.t4g.small` | 240 hours x $0.051/hour | 12.24 | P3 |
| NAT gateway | 240 hours x $0.059/hour | 14.16 | P1 |
| NAT gateway public IPv4 | 240 address-hours x $0.005/hour | 1.20 | P5 |
| Internal NLB | 240 hours x $0.0252/hour | 6.05 | P4 |
| Worker root disks, Kafka and logging gp3 | (2 x 20 + 5 + 5 + 1 + 1) GB x $0.096/GB-month x 10/30 | 1.66 | P1 |
| RDS gp3 storage | 20 GB x $0.138/GB-month x 10/30 | 0.92 | P3 |
| RDS-managed administrator secret | 1 secret x $0.40/month x 10/30 | 0.13 | P6 |
| **Capacity subtotal, before usage** | **240 hours** | **161.74** | |

Private workers, private RDS, and the internal NLB do not require additional
public IPv4 addresses. Kafka compute is included in the workers, not a separate
managed Kafka service. No separate VPC Link hourly fee is assumed for this HTTP
API integration; this is not a PrivateLink interface endpoint. NLB, request,
and data-transfer charges still apply. No domain purchase or separate public
certificate fee is included for API Gateway's default HTTPS endpoint.
Loki/Grafana/Alloy compute shares the existing workers; logging adds 7 GiB of
claims (5 GiB Loki, 1 GiB Grafana, 1 GiB Alloy), approximated as 7 GB for pricing.
These additions reuse the dated catalog rates below, not a new price check.

### Illustrative usage, not measured traffic

The following inputs are a low-volume example, not a workload forecast or limit.
NLCU usage varies by connections and processed bytes; 240 NLCU-hours means an
average of one NLCU over the run. Monthly storage quantities are averages.
Totals use unrounded amounts.

| Usage charge | Example quantity and rate | Ten days (USD) | Source |
| --- | --- | ---: | --- |
| NLB capacity | 240 used NLCU-hours x $0.006 | 1.44 | P4 |
| NAT processing | 20 GB x $0.059/GB | 1.18 | P1 |
| HTTP API requests | 100,000 billable request units x $1.25/million | 0.13 | P7 |
| CloudWatch log ingestion | 5 GB x $0.70/GB | 3.50 | P8 |
| CloudWatch log storage | 1 GB x $0.03/GB-month x 10/30 | 0.01 | P8 |
| ECR private image storage | 5 GB x $0.10/GB-month x 10/30 | 0.17 | P9 |
| SES outbound recipients | 1,000 x $0.10/1,000 recipients | 0.10 | P10 |
| S3 objects/state and requests | 1 GB x $0.025/GB-month x 10/30; 10,000 PUT/LIST x $0.005/1,000; 100,000 GET x $0.0004/1,000 | 0.10 | P11 |
| Loki S3 logs and requests | 5 GB average x $0.025/GB-month x 10/30; 10,000 PUT/LIST x $0.005/1,000; 100,000 GET x $0.0004/1,000 | 0.13 | P11 |
| Secrets Manager API calls | 100 x $0.05/10,000 calls | <0.01 | P6 |
| **Example usage subtotal** | | **6.75** | |
| **Capacity plus example usage** | | **168.49** | |

The accepted planning allowance is **US$200** for this light-demo scenario,
replacing the US$150 target, which was below the capacity subtotal alone. It is
not a cap or a claim that heavier use fits. Accepting the allowance authorizes
planning, not provisioning; review the saved plans before approving any apply.
Every extra full-stack day adds approximately **US$16.17 plus usage**. Budget alerts and expiry tags do not stop resources.

### Additional charges and retained data

- C1: Internet egress and regional/cross-AZ data transfer are excluded from the
  example. Traffic volume and actual paths are not measured. The single-AZ NAT
  and cross-zone NLB can add transfer charges. S3 traffic routed through the
  same-region gateway endpoint avoids NAT processing, not S3 storage/request fees.
- C2: RDS T4g surplus CPU credits cost $0.075/vCPU-hour (P3). Sustained CPU can
  increase the database bill. Unlike the workers' Standard credit setting, RDS
  burstable instances use Unlimited mode. No surplus usage is modeled here.
- C3: Extra backups, snapshots, storage growth, log queries, enhanced image
  scanning, email data transfer/add-ons, and KMS requests are excluded. Basic ECR
  scan-on-push has no additional scan fee. The estimate assumes AWS-managed keys,
  not an extra customer-managed KMS key.
- C4: Retained data continues billing after compute teardown. Kafka's retained
  5 GB and logging's retained 7 GB gp3 total approximately $1.15/month (P1).
  Loki log objects also persist after the compactor stops; seven-day retention
  depends on a running compactor and is not an S3 teardown policy.
  RDS backup storage above the
  included allocation, including retained final snapshots after deletion, is
  $0.095/GB-month (P3). S3 state versions, objects, and tagged ECR images persist
  until explicitly cleaned up. Deletion protection and `ExpiresOn` do not
  schedule shutdown or erase data.

### Sources

- P1: [EC2 Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/20261107002852/ap-southeast-1/index.csv), published 7 Nov 2026. Usage types `APS1-BoxUsage:t3.xlarge`, `APS1-NatGateway-Hours`, `APS1-NatGateway-Bytes`, and `APS1-EBS:VolumeUsage.gp3`.
- P2: [EKS Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEKS/20261027191639/ap-southeast-1/index.json), published 27 Oct 2026. SKU `FCCKZFRYQFAGPWQN`, standard support, not Auto Mode or extended support.
- P3: [RDS Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonRDS/20261029070635/ap-southeast-1/index.json), published 29 Oct 2026. SKUs `EGTBSGEA54PMU4UD` (PostgreSQL instance), `FXXDJWB23UZKQHCN` (gp3), `MP6SPJ889449AMT3` (T4g surplus CPU credits), and `9XF3U4K57MG3MUFQ` (excess backup storage).
- P4: [ELB Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSELB/20261105220241/ap-southeast-1/index.json), published 5 Nov 2026. SKUs `WUD77WXSFRU628UM` (NLB hours) and `YEREUZKF8ZSAFR8M` (used NLCU-hours).
- P5: [VPC Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonVPC/20261105181945/ap-southeast-1/index.json), SKU `C8D8VMQRDHMB9KRU` (in-use public IPv4).
- P6: [Secrets Manager Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSSecretsManager/20260911124610/ap-southeast-1/index.json), SKUs `J5PMSXRJZ3HNHFEV` (secret) and `KAUJ7G87ND7HD8N8` (API calls).
- P7: [API Gateway Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonApiGateway/20261013003044/ap-southeast-1/index.json), SKU `HTX6VZU3YCMMNRM5`, first 300 million monthly HTTP API requests. [Request metering and private integration pricing](https://aws.amazon.com/api-gateway/pricing/) applies; a large request can consume multiple billing units.
- P8: [CloudWatch Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonCloudWatch/20261104213532/ap-southeast-1/index.json), SKUs `ZEKCE7VGMSM5XZPW` (Standard ingestion) and `RKCBFFXUWXG8KUQY` (log storage). Seven-day retention limits storage duration, not ingestion charges.
- P9: [ECR Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECR/20260911124425/ap-southeast-1/index.json), SKU `7V2YHG7PQ82BEXNB`; [ECR pricing](https://aws.amazon.com/ecr/pricing/) covers basic versus enhanced scanning.
- P10: [SES Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonSES/20261104174109/ap-southeast-1/index.json), SKU `94WZY2MPPRKE3CPE` (EC2-origin sending to external recipients), excluding optional sending add-ons.
- P11: [S3 Singapore catalog](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonS3/20261104174445/ap-southeast-1/index.json), SKUs `M6MTARCQFUQBQ2V3` (Standard storage, first 50 TB), `VHRJWKRHEA6VHEFY` (PUT/LIST), and `T469UNPYX78434EG` (GET).

[Network transfer](https://aws.amazon.com/vpc/pricing/),
[RDS burst credits](https://aws.amazon.com/rds/postgresql/pricing/),
[RDS-managed secrets](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-secrets-manager.html),
and [KMS pricing](https://aws.amazon.com/kms/pricing/) describe additional
billing conditions. Catalog links are version-pinned so these rates remain
auditable when current prices change.

## Release automation

[Apply/publication and release operations](../../docs/runbooks/aws-releases.md)
describe the versioned non-secret S3 configuration contract, reviewed-plan
publication recovery, regenerated overlays, scoped deployment identity,
protected runner setup, and serialized release workflow. Application CI does
not read Terraform state or create workload Secrets.

## Local validation (no AWS resources)

Requirements: Terraform 1.13+, the repository's Go version, Aqua's pinned
Kubernetes/Helm tools, and OpenSSL supporting `pkey -check` for the credential
tests. The verifier also checks Go formatting and tooling-module tests.
Operational code and tests live in the existing Go tooling module; its YAML
test parser is pinned in scripts/go.mod. No Python or virtualenv is required.
Live setup additionally requires the AWS CLI.

From the repository root, the repeatable check is:

```sh
sh scripts/aws/verify.sh
```

It prints the commands and ends with a single PASS only if every check succeeds.
Set `TF_BIN` to an explicit Terraform binary path when it is not on PATH. The
same checks can be run individually:

```sh
terraform -chdir=infra/aws init -backend=false -input=false
terraform -chdir=infra/aws fmt -check -recursive
terraform -chdir=infra/aws validate
terraform -chdir=infra/aws test
make k8s-tools aws-tools
PATH="$PWD/.tools/bin:$PWD/.tools/aqua/bin:$PATH" \
  K8S_RENDER_TESTS=1 GOWORK=off go -C scripts test -mod=readonly ./aws -v
```

Go CLI fixtures mock all AWS calls; Terraform tests mock the upstream VPC/EKS modules. They exercise root
resource safeguards, render the actual Kubernetes bases with a non-default
hostname, preserve the private RPC boundary, mount RDS certificates in all six
services and migration Jobs, and preserve private database passwords on retry.
They also invoke the actual Go operator against the generated AWS overlay.
For the full pinned Helm/IRSA/claim rendering check, install the pinned Helm tool
and cache the checksum-verified Loki chart, then run the overlay tests with
`K8S_RENDER_TESTS=1`; this invokes `render observability` without a cluster.
They do not validate upstream module internals, AWS authorization, pod injection,
image builds, RDS SQL execution, DNS, email delivery, or actual application flows.
An optional real PostgreSQL 16+ test runs when `AWS_SQL_TEST_SOCKET` points to a
fresh, disposable Unix-socket server on port 55439, initialized with superuser
`fixture_owner`. Set `PSQL` to that installation's client. It creates test roles
and databases, tests non-superuser provisioning/retries, and rejects mismatched
ownership. Never point this fixture test at an existing database. It omits RDS
PostGIS setup, which still requires deployment validation.

Pinned modules: VPC 6.7.3, EKS 21.29.0. Commit provider lock files. Controller IAM
policy source:
https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v3.6.2/docs/install/iam_policy.json

SHA-256: `16f232c9d9f79366fe949c4550ad517a202380058a9e48d45a4e215044a20a6a`.
The Helm chart is pinned to 3.6.2, matching controller v3.6.2. Review its upstream
release and IAM permissions before an upgrade. Istio remains pinned to 1.31.2
through the existing aqua configuration and admission policy.

## 1. Private inputs and state (after approval)

Run commands from the repository root. Choose an authenticated AWS profile using
`AWS_PROFILE`; do not put credentials or a personal profile name in shared code.
All commands in the remaining sections are future operator instructions, not
commands automatically run by validation.

```sh
umask 077
mkdir -p .local/aws
cp infra/aws/terraform.tfvars.example infra/aws/terraform.tfvars
```

Edit the ignored tfvars file with the expected account, your current public
IPv4 /32, and the teardown date ten days after deployment. The public HTTPS
hostname is a Terraform output, not a domain input. Do not use example values.
The public EKS management API allows only that /32. The application API Gateway
is publicly reachable and uses FoC's application authentication. If your IP changes, update and review the
Terraform plan. Private nodes reach the API through its private endpoint.

Set `ACCOUNT_ID`, `AWS_REGION`, and `DEPLOYMENT_NAME` to match the tfvars file.
Bootstrap the state bucket separately, reviewing its plan before applying:

```sh
terraform -chdir=infra/aws/state init -input=false
terraform -chdir=infra/aws/state plan \
  -var="account_id=$ACCOUNT_ID" -var="region=$AWS_REGION" \
  -var="name=$DEPLOYMENT_NAME" -out=backend.tfplan
# Creates a bucket: run only after approval of this saved plan.
terraform -chdir=infra/aws/state apply backend.tfplan
```

The bootstrap stack uses local state. Preserve its state securely and back it up
to the new private state bucket. Do not delete that bucket during app teardown.
Create ignored `infra/aws/backend.hcl` with the returned bucket name:

```hcl
bucket       = "REPLACE_WITH_STATE_BUCKET"
key          = "foc/demo/terraform.tfstate"
region       = "ap-southeast-1"
encrypt      = true
use_lockfile = true
```

Then initialize the main stack's remote backend:

```sh
terraform -chdir=infra/aws init -reconfigure -backend-config=backend.hcl
terraform -chdir=infra/aws plan -out=deployment.tfplan
# Creates billable resources: run only after approval of this saved plan.
terraform -chdir=infra/aws apply deployment.tfplan
terraform -chdir=infra/aws output -json deployment > .local/aws/deployment.json
```

Plans/state can contain sensitive infrastructure information. Keep them private.
Never apply with a different account, region, or stale plan. The provider checks
the expected account. S3 backend credentials need access to both the state key
and its `.tflock` key. Do not disable locking to work around access denials.

## 2. Render AWS configuration

After apply, obtain the AWS RDS trust bundle and generate ignored overlay files:

```sh
curl --fail --location --proto '=https' --tlsv1.2 \
  https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem \
  --output .local/aws/global-bundle.pem
make aws-tools
.tools/bin/aws render-overlay \
  --outputs .local/aws/deployment.json --rds-ca .local/aws/global-bundle.pem
kubectl kustomize .local/aws/production > .local/aws/rendered.yaml
```

The renderer does not contact AWS or apply anything. It keeps the tracked generic
production template unchanged and writes only under `.local/`. Do not mix these
artifacts between accounts/checkouts. The generated configuration supplies NLB
private HTTP ingress, a TargetGroupBinding, exact API Gateway browser origins,
regional S3, Order's IRSA annotation, Kafka's gp3 class, and RDS CA mounts. Application database URLs
use `sslmode=verify-full`. Existing internal Service URLs and mesh policies remain.

## 3. Bootstrap the cluster platform

Create a private kubeconfig; do not replace the user's global context:

```sh
export KUBECONFIG="$PWD/.local/aws/kubeconfig"
export K8S_CONTEXT="$DEPLOYMENT_NAME"
aws eks update-kubeconfig --region "$AWS_REGION" --name "$DEPLOYMENT_NAME" \
  --alias "$K8S_CONTEXT" --kubeconfig "$KUBECONFIG"
make k8s-tools
export AQUA_ROOT_DIR="$PWD/.tools/aqua"
k() { aqua exec -- kubectl --context "$K8S_CONTEXT" "$@"; }
k apply -f infra/k8s/platform/gateway-api/standard-install.yaml
k apply -f infra/k8s/platform/namespaces.yaml
k apply -f infra/k8s/platform/admission.yaml
# Administrator-owned log access is separate from release reconciliation.
k apply -k infra/k8s/base/observability-rbac
ISTIOCTL=$(aqua which istioctl)
ISTIO_CHARTS="$(dirname "$(dirname "$ISTIOCTL")")/manifests/charts"
aqua exec -- helm upgrade --install istio-base "$ISTIO_CHARTS/base" \
  --kube-context "$K8S_CONTEXT" -n istio-system --wait --timeout 10m
aqua exec -- helm upgrade --install istiod "$ISTIO_CHARTS/istio-control/istio-discovery" \
  --kube-context "$K8S_CONTEXT" -n istio-system \
  -f infra/k8s/platform/istiod-values.yaml --wait --timeout 10m
```

Wait for Gateway API CRDs to be Established before applying Gateway resources.
Verify `aws-ebs-csi-driver`, `vpc-cni`, CoreDNS, and kube-proxy are healthy. A
Restricted Pod Security namespace needs a compatible Istio CNI policy instead of
silently disabling admission or mesh capture. This profile uses Istio's standard
NET_ADMIN/NET_RAW initializer as documented by the existing platform.

Set `INGRESS_ROLE_ARN` and `VPC_ID` from the non-secret deployment outputs:

```sh
aqua exec -- helm repo add eks https://aws.github.io/eks-charts
aqua exec -- helm repo update eks
aqua exec -- helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
  --version 3.6.2 --kube-context "$K8S_CONTEXT" -n kube-system \
  --set clusterName="$DEPLOYMENT_NAME" --set region="$AWS_REGION" --set vpcId="$VPC_ID" \
  --set serviceAccount.name=aws-load-balancer-controller \
  --set-string serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn="$INGRESS_ROLE_ARN" \
  --wait --timeout 10m
k apply -k .local/aws/production/dependencies
```

Install the load-balancer controller and wait for its webhook before deploying
TargetGroupBinding resources. Terraform creates the internal NLB, its port-80
listener, and the IP target group. The generated gateway stage binds that group
to `istio-ingress/foc-public-istio` service port 80. The Service stays ClusterIP;
Kubernetes does not create another load balancer. The private HTTP listener keeps
its existing `https` section name so the base HTTPRoute parentRefs remain valid.
It has no hostname or TLS certificate reference.

API Gateway uses a `$default` stage and route, forwards all HTTP methods, and
maps `overwrite:path` to `$request.path` so RPC paths have no stage prefix. FoC
retains application authentication; the proxy adds no separate JWT authorizer.
The frontend and APIs share the default execute-api HTTPS origin. S3 CORS uses
that exact origin. Do not forward Authorization/cookies to S3.

Verify NLB targets become healthy after the gateway is deployed. The API URL
exists before Kubernetes targets are ready and can initially return errors.
Limits are 30 seconds per integration and 10 MB payloads. Image bytes go directly
to S3. Long RPCs, large responses, cookies, RPC errors, and page/asset responses
still need live verification. Local TLS port-forward smoke commands assume the
original in-cluster HTTPS profile and are not acceptance tests for this profile.

## 4. Provision the six databases and remaining Secrets

Read the RDS-managed admin secret directly into a private file, never chat or
terminal output. Set `DATABASE_SECRET_ARN` from deployment outputs:

```sh
aws secretsmanager get-secret-value --secret-id "$DATABASE_SECRET_ARN" \
  --region "$AWS_REGION" --query SecretString --output text > .local/aws/rds-admin.json
make aws-tools
.tools/bin/aws prepare-database --outputs .local/aws/deployment.json \
  --admin-secret .local/aws/rds-admin.json
k apply -f .local/aws/db/secrets.json
k apply -f .local/aws/db/job.json
k -n foc wait --for=condition=complete job/rds-provision --timeout=300s
```

This explicit Job creates isolated roles/databases and installs Supplier's
PostGIS and pg_trgm. PostgreSQL 16+ requires explicit SET ROLE permission to
assign database ownership. The Job temporarily enables administrator INHERIT/SET
membership in the service roles, validates existing database ownership, and
disables those temporary permissions after extension setup. RDS can record
`rdsadmin` as grantor with `ADMIN=false` even when provisioning retries work.
Check that INHERIT/SET are disabled and verify an actual retry rather than
requiring the ADMIN flag. If the Job fails mid-provisioning,
those temporary permissions may remain until a successful retry or explicit
administrator cleanup. It does not drop data or rotate existing roles. It is NOT
part of the automatic deployment stages. On retry, first inspect the previous
Job's status. Never delete a running Job. After success, remove the completed
Job, its `rds-provision-admin` Secret and unused ServiceAccount, and remove the
local admin JSON. Keep the six private service credential files for retries.
Treat failure logs as potentially sensitive. Do not stream them into chat.

For SES API delivery, set `ses_sender_email` to a bare, verified email address
in the Terraform inputs. Review the plan before applying: User and Communication
have separate IAM roles and inline policies permitting only `ses:SendEmail` and
`ses:SendRawEmail`, scoped to that regional identity and `ses:FromAddress`. Each role
trusts only its corresponding `foc:user-service` or `foc:communication-service`
ServiceAccount and the STS audience. User's original role remains available while
existing deployments still send email directly. Order's S3 permissions are unchanged.
The adapter sends raw MIME through SESv2's `SendEmail` operation. AWS also
requires `ses:SendRawEmail` for that content: a live workload request with only
`ses:SendEmail` was rejected with `AccessDeniedException` for `ses:SendRawEmail`.
The API operation name alone does not establish the required IAM permissions.

After the approved apply, refresh `.local/aws/deployment.json` and rerender the
overlay. The renderer sets `EMAIL_PROVIDER=ses`, `EMAIL_FROM`, and the Communication
Service IRSA annotation, and removes Communication's SMTP Secret reference. No static
AWS keys or SMTP login are needed. The operator checks the rendered reference
before requiring `smtp`. Existing SMTP overlays and local Mailpit are unchanged.
A null/default `ses_sender_email` retains SMTP and creates no email role.
The deployment output exposes `user_email_role_arn` and
`communication_email_role_arn`; regenerate the output JSON before rendering.
The roles are named `<name>-user-email` and `<name>-communication-email`.
Communication is added independently without replacing User's existing role.
The renderer uses Communication's ARN; the User ARN output does not itself change
User's ServiceAccount annotation.

Before applying in an existing environment, inspect the email role and policy
state bindings. `aws_iam_role.email[0]` and `aws_iam_role_policy.email[0]` must
refer to `<name>-user-email`; the corresponding `communication_email[0]`
resources must refer to `<name>-communication-email`. An environment that applied
the earlier Communication-only configuration may instead bind `email[0]` to
Communication's role. Source changes do not migrate those bindings: have the
infrastructure operator reconcile them under the backend lock, then review a
fresh plan. Do not apply a plan that replaces an existing email role as part of
this migration. A correctly bound environment with both roles already managed
should show no changes to either role or its policy.

SES sender verification, production access, workload authorization, and actual
mailbox delivery are separate checks. A policy simulation is not proof of live
sending. After deployment, verify the workload's role and send an approved test
message before claiming delivery. SES errors are returned, never redirected to
SMTP or Mailpit. To return to SMTP, supply working SMTP configuration/credentials
and unset `ses_sender_email`; removing SES does not provide an SMTP login.

Provide the remaining Secrets through private files or the approved secret
manager, following docs/runbooks/kubernetes.md:

- `foc-observability/grafana-admin`: keys `username` and `password`, supplied by
  the operator. Retain the credentials privately; do not store them in Terraform
  or release configuration. Given private files containing the chosen values
  without trailing newlines, create it with:

  ```sh
  k -n foc-observability create secret generic grafana-admin \
    --from-file=username=.local/aws/grafana/username \
    --from-file=password=.local/aws/grafana/password
  ```

  Existing credentials remain authoritative on retry. Grafana has no public
  Gateway route; access it with the existing operator port-forward command.
- `smtp` (SMTP mode only): SMTP_ADDR, SMTP_FROM, SMTP_USERNAME, SMTP_PASSWORD. Use SES port 587
  with STARTTLS. Verify an email identity in the same AWS region separately from
  Terraform, and set SMTP_FROM to that address. No domain identity is configured
  for the AWS-owned API hostname. SES production access requires AWS approval;
  sandbox mode requires verified recipients. Check actual delivery, including
  spam/DMARC behavior when using an address on a domain you do not control.
  Do not silently fall back to Mailpit.
- `moderation-llm`: `CMS_LLM_API_KEY`, supplied privately by the operator.
  The [moderation runbook](../../docs/runbooks/content-moderation.md) covers its
  database, model settings, release and workflow verification.
- `jwt-signing`: `jwt-signing-private.pem`, generated privately and retained.
- `telegram`: TELEGRAM_BOT_TOKEN, empty to disable. Never run two pollers with
  the same bot token.
- `kafka-ui-auth`: SPRING_SECURITY_USER_NAME and SPRING_SECURITY_USER_PASSWORD.
- No `foc-public-tls` Secret is required for this private HTTP ingress. The
  deployment operator checks certificate Secrets only for rendered HTTPS
  listeners. The original local and custom-domain HTTPS overlays are unchanged.

Never put a password in a shell argument. Use `kubectl create secret ...
--from-env-file=<private-file>` or `--from-file` and avoid printing the manifest.
Do not configure static AWS credentials for Order; IRSA supplies credentials.
SES SMTP credentials are separate from Order's AWS role and must be obtained
securely. This package does not create an IAM user or SMTP access key.

Use CLI to check the regional sender and account state. Set `SES_SENDER_EMAIL`
privately to the verified From address (not a display-name/email combination):

```sh
aws sesv2 get-email-identity --email-identity "$SES_SENDER_EMAIL" \
  --region "$AWS_REGION" --query '{Verified:VerifiedForSendingStatus,Status:VerificationStatus}'
aws sesv2 get-account --region "$AWS_REGION" \
  --query '{Production:ProductionAccessEnabled,Review:Details.ReviewDetails}'
```

If the identity is missing, request verification with `aws sesv2
create-email-identity --email-identity "$SES_SENDER_EMAIL" --region "$AWS_REGION"`
and have the mailbox owner click the AWS verification link. A verified sender
alone does not remove sandbox recipient restrictions. Request production access
with `aws sesv2 put-account-details` using truthful transactional use-case and
website information. AWS review can remain pending or be denied; do not claim
production readiness until `ProductionAccessEnabled` is true. A public project
information URL is not evidence that the application has been deployed.

## 5. Publish images and deploy

ECR repositories have immutable tags. Use a unique commit/build tag every time;
rebuilding an existing tag will fail. All nine images must target linux/amd64
because the worker nodes are x86_64, even when building on Apple Silicon.
Set `K8S_IMAGE_PREFIX` from deployment outputs. Export the four `K8S_LOKI_*`
settings printed by the renderer: bucket and role come from
`loki_bucket`/`loki_role_arn`, region matches `AWS_REGION`, and the generated
storage class is `foc-gp3`. The renderer prints values without changing your
shell environment. Re-export them after rerendering or changing deployments:

```sh
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
export DOCKER_DEFAULT_PLATFORM=linux/amd64
export K8S_OVERLAY="$PWD/.local/aws/production"
export K8S_IMAGE_TAG="$(git rev-parse --short=12 HEAD)-build1"
export K8S_PUSH_IMAGES=1
export K8S_BOOTSTRAP_SUPERADMIN_EMAIL="REPLACE_WITH_APPROVED_ADMIN_EMAIL"
make k8s-deploy
```

The operator applies gateway, dependencies, observability, initialization, then applications.
Its production provisioning stage is intentionally empty. It waits for migration
and Kafka initialization Jobs and rejects template origins/local image tags.
Terraform does not claim that successful resource creation means FoC works.

Use `public_origin` from Terraform's deployment output as the browser address.
No DNS record or domain purchase is required. Do not expose the NLB, internal
service ports, or Kafka UI publicly. API Gateway access logs omit paths, query
strings, bodies, cookies, and authorization headers.

## 6. Acceptance after deployment

Verify the exact public HTTPS URL with certificate validation, frontend loading,
all public health RPCs, real magic-link email/login, a signed image upload and
read-back, and a representative order flow supported by the current main branch.
Prove internal public-key and Credit reservation RPCs are not publicly exposed,
and test Order-to-Credit versus unauthorized in-cluster callers. Inspect actual
pod injection and storage persistence. Keep a repeatable script/report and any
UI evidence. Existing local-only smoke checks are not proof of external HTTPS.
The repository still explicitly does not claim the complete D3 demo workflow.
Verify Loki's assumed role and encrypted claims, query a unique application log
marker through authenticated Grafana, and test log readability after Loki pod
replacement. Offline rendering does not prove S3 writes or log delivery.

## 7. Teardown after ten days (separate approval)

Remove the Kubernetes target binding before destroying Terraform infrastructure.
Terraform owns API Gateway, its VPC Link, and the internal NLB. Kafka's disk is
created by Kubernetes and is not directly tracked by this Terraform stack.

1. Decide which database, images, objects, Kafka and logging data must be retained.
   Record snapshot IDs, bucket names, and retained disk IDs with their ongoing cost.
2. Delete the manually managed `istio-ingress/foc-public` TargetGroupBinding
   while the load-balancer controller is still running. Wait for deregistration
   and finalizer removal, then delete the `foc-public` Gateway and its generated
   Service. Do not delete unrelated controller-generated bindings. The NLB and
   target group stay until the approved Terraform teardown.
3. Delete Kafka's StatefulSet/PVC after the data decision. Its Retain policy
   leaves the PV/EBS disk. Explicitly preserve or delete that disk; deleting the
   EKS cluster does not dispose of it.
   For logging, stop Alloy/Grafana, then uninstall the Helm release with
   `helm --kube-context "$K8S_CONTEXT" uninstall loki -n foc-observability`.
   Inventory Loki, Grafana and Alloy PVCs/PVs and their EBS IDs before deleting
   claims: all use Retain, including Helm's Loki claim. Preserve or explicitly
   delete those disks and Loki's S3 log objects after the data decision. Stop the
   compactor before final object deletion; a stopped compactor performs no retention.
4. Empty only the intended ECR repositories and S3 Order/Loki buckets, plus the
   moderation evidence bucket when enabled, if their data deletion was approved. Do not enable force deletion as a timeout workaround.
5. Disable RDS deletion protection through a reviewed Terraform change/apply.
   Keep final snapshots by default. Ensure the chosen final snapshot name does
   not collide with a snapshot from a previous deployment.
6. Review `terraform -chdir=infra/aws plan -destroy -out=teardown.tfplan`, then
   apply that saved teardown plan only with explicit deletion authorization.
7. Verify EKS, worker EC2/volumes, NAT/EIP, RDS instance, and NLB resources are
   gone. Inventory retained snapshots, disks, S3 objects/state versions, images,
   log groups, and Secrets Manager secrets. Budget figures lag actual usage.

The state bootstrap bucket is deliberately separate and protected. Retain it for
recovery/audit until its removal is separately approved. Do not erase local
credentials or state before confirming the intended teardown succeeded.

## Reports and appeals resource preparation

`enable_moderation_service = true` prepares resources for `moderation-service`
(reports and appeals), not `content-moderation-service` (LLM review assessment).
It defaults to false and does not deploy a workload or change existing image
builds, Gateway routes, Kafka topics, or application migrations.

| Resource | Scope |
| --- | --- |
| ECR | One additional immutable `<name>/moderation-service` repository and untagged-image lifecycle policy. No automatic image deletion on destroy. |
| PostgreSQL | `prepare-database` includes `moderation_db`, owned by login `foc_moderation`, and Secret `foc/moderation-db`. It reuses the private RDS instance with verified TLS, denies PUBLIC connections, and retains existing service credentials. Terraform itself does not create the logical database or password. |
| Evidence S3 | One separate bucket, AES256 encryption, all public access blocked, HTTPS-only access, and GET/HEAD/PUT CORS restricted to the application origin. Objects are retained by default. |
| Workload IAM | `<name>-moderation-evidence`, assumed only by `system:serviceaccount:foc:moderation-service` with the STS audience. Allows only GetObject, PutObject, and DeleteObject under `evidence/*` in the evidence bucket. HeadObject uses GetObject permission; DeleteObject supports the runtime's 24-hour orphan-upload cleanup. No listing, other prefixes/buckets, mail, database administration, ECR push, or infrastructure permissions. |
| Release IAM | The CI root has a separate opt-in with the same name. It adds only this exact ECR repository to the existing release image permissions. It does not extend the workload's permissions or enable release builds. |
| Compute and networking | Reuse EKS, RDS, Kafka, API Gateway, and the existing mesh. No new node group, database instance, broker, NAT, public load balancer, or public database port. |

The main Terraform root adds nine resources when enabled: two ECR resources,
five S3 resources, and two IAM resources. `terraform output -json moderation`
exposes non-secret storage, image, identity, and database names. The existing
`deployment` output carries a boolean `moderation_enabled` for database
preparation. Refresh that output before running the existing `prepare-database`
command; retain the existing private database directory so enabling moderation
cannot rotate the other six credentials. Preparing files does not apply them;
review and explicitly run the provisioning Job separately.

### Assumptions and deployment boundaries

- The initial evidence-storage choice is a dedicated bucket rather than a prefix
  in User's bucket (`docs/adr/moderation-evidence-storage.md`). The runtime must use these bucket settings before receiving
  this role. This does not migrate existing evidence or grant access to User's
  bucket. If the extraction uses shared storage, reconcile that contract before
  enabling it, rather than broadening this role to all application buckets.
- The runtime already cleans unattached uploads after 24 hours, so its role needs
  object deletion. IAM cannot distinguish an orphan from attached evidence: the
  role can delete any object under its evidence prefix. Retention of attached
  evidence remains undecided. There is no bucket-wide lifecycle expiry, public
  read, or additional KMS key. Review audit/privacy retention before real use.
- Existing demo capacity is a starting assumption, not a measured guarantee.
  The extraction's shared workload component gives its one application replica
  requests of 50m CPU and 64Mi memory, a 256Mi memory limit, and no CPU limit.
  Istio's sidecar has separate overhead. These are configured defaults, not
  measured requirements. Replica sizing, RDS load, Kafka partitioning, and
  service latency still need evaluation. This change does not resize them.
- PostgreSQL credentials and Kubernetes/Istio caller authorization are separate
  from AWS IAM. Internal User/Order RPC access must be restricted to the required
  methods. Kafka outbox/consumer wiring uses the existing broker, not MSK/IAM
  broker administration. No new SES, LLM, or model-provider credentials are
  assumed for reports and appeals.
- Runtime/release integration is not included. The extraction branch already
  supplies its Docker target, migrations, Deployment/Service/ServiceAccount,
  public RPC routes, Kafka outcome contract/topic, and health checks. These must
  be present in the release source; AWS role annotation, S3 settings, RDS CA
  mounts, release inventories, and internal caller authorization still need
  integration and verification. Current release configuration does not
  publish these optional resource outputs. Do not interpret enabled Terraform
  resources as a deployable or verified service.

After an approved apply, prepare and provision the isolated database, integrate
those runtime contracts, review the rendered manifests, then verify evidence
upload/read authorization, database isolation, RPC caller restrictions, Kafka
retry/idempotency, and user-visible report/appeal flows before enabling traffic.
Do not use a full-environment apply just to add the service without reviewing
all unrelated plan changes. Retained evidence needs an explicit deletion
choice during teardown, just like the other application buckets.
