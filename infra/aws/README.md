# Seven-day AWS demo

This is a preparation package, not evidence of a running deployment. No command
below should create AWS resources, publish images, or change a cluster without
operator approval. The account's quotas and organization restrictions, regional
engine availability, actual prices, domain ownership, and SES sending status
must be checked before apply. `terraform validate` and the mocked tests do not
prove that the school permits deployment.

## Decisions log

- D1: Two On-Demand `t3.xlarge` workers in private subnets across two AZs, one NAT
  gateway, and a public TCP NLB. The NAT is a single-AZ failure dependency and
  cross-AZ traffic can cost extra. The NLB is created by the Kubernetes controller,
  not directly by Terraform.
- D2: Single-AZ `db.t4g.small` PostgreSQL, one Kafka broker with a 5 GiB encrypted
  gp3 claim, and one application replica each. This is a demo, not high availability.
- D3: T3 standard credits avoid surplus-credit charges but can throttle sustained
  CPU. Node updates use MINIMAL strategy to avoid default surge capacity. Expect
  downtime during node updates, particularly for single-replica workloads and Kafka.
- D4: IRSA supplies short-lived AWS credentials only to Order, the EBS CSI driver,
  and the load-balancer controller. Application AWS access keys are not needed.
  RDS manages the administrator password in Secrets Manager. Terraform does not
  read that password or generate SMTP/JWT/application database credentials.
- D5: State uses a separate versioned, encrypted S3 bucket with native S3 locking.
  `ExpiresOn` is a tag only. It does not delete or stop anything.
- D6: RDS deletion protection and a final snapshot, S3 `force_destroy=false`, ECR
  `force_delete=false`, and Kafka's Retain policy require deliberate data decisions
  during teardown. Retained resources continue accruing charges.
- B1: A real hostname, DNS access, TLS certificate/renewal owner, SMTP credentials,
  bootstrap email, expiry date, and approved AWS account must be supplied privately.
  These inputs do not block offline preparation. They block a real deployment.
- B2: Quota/service restrictions and live sizing remain unverified. Eight vCPUs are
  needed for initial workers. No quota-inspection denial is permission to bypass
  organization policy. If deployment is denied, stop and ask the account owner.

The working cost estimate is approximately US$111-117 for 168 hours plus traffic,
logs, state, Secrets Manager, retained snapshots/disks, taxes, and other usage.
It has not been independently price-verified. US$150 is a planning target, not a
cap. A monthly AWS Budget alert does not stop spending or enforce a seven-day
limit. Re-estimate using current Singapore pricing before apply.

## Local validation (no AWS resources)

Requirements: Terraform 1.13+, kubectl, and Python 3.9+. The scripts themselves
use only Python's standard library. Tests also require PyYAML. Use an explicit
checkout-local virtualenv so different shell PATHs cannot select an interpreter
without the test dependency.

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
python3 -m venv .tools/aws-python
.tools/aws-python/bin/python -m pip install -r scripts/aws/requirements-test.txt
.tools/aws-python/bin/python -m unittest discover -s scripts/aws -p 'test_*.py' -v
```

Tests mock all AWS calls and the upstream VPC/EKS modules. They exercise root
resource safeguards, render the actual Kubernetes bases with a non-default
hostname, preserve the private RPC boundary, mount RDS certificates in all five
services and migration Jobs, and preserve private database passwords on retry.
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

Edit the ignored tfvars file with the expected account, an owned hostname, your
current public IPv4 /32, and the actual teardown date. Do not use example values.
The public API allows only that /32. If your IP changes, update and review the
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
python3 scripts/aws/render_overlay.py \
  --outputs .local/aws/deployment.json --rds-ca .local/aws/global-bundle.pem
kubectl kustomize .local/aws/production > .local/aws/rendered.yaml
```

The renderer does not contact AWS or apply anything. It keeps the tracked generic
production template unchanged and writes only under `.local/`. Do not mix these
artifacts between accounts/checkouts. The generated configuration supplies NLB
annotations, the hostname/TLS Secret, exact browser origins, regional S3, Order's
IRSA annotation, Kafka's gp3 class, and RDS CA mounts. Application database URLs
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

The ingress Service annotations select an internet-facing IP-target NLB in both
public subnets. The Service overlay removes Istio's status-port 15021 from public exposure;
verify the resulting NLB has only the HTTPS 443 listener after deployment. The
NLB uses TCP passthrough; do not
configure a second TLS termination layer without reviewing the Gateway design.

## 4. Provision the five databases and remaining Secrets

Read the RDS-managed admin secret directly into a private file, never chat or
terminal output. Set `DATABASE_SECRET_ARN` from deployment outputs:

```sh
aws secretsmanager get-secret-value --secret-id "$DATABASE_SECRET_ARN" \
  --region "$AWS_REGION" --query SecretString --output text > .local/aws/rds-admin.json
python3 scripts/aws/prepare_database.py --outputs .local/aws/deployment.json \
  --admin-secret .local/aws/rds-admin.json
k apply -f .local/aws/db/secrets.json
k apply -f .local/aws/db/job.json
k -n foc wait --for=condition=complete job/rds-provision --timeout=300s
```

This explicit Job creates isolated roles/databases and installs Supplier's
PostGIS and pg_trgm. PostgreSQL 16+ requires explicit SET ROLE permission to
assign database ownership. The Job temporarily enables administrator INHERIT/SET
membership in the service roles, validates existing database ownership, and
disables those temporary permissions after extension setup. ADMIN membership is
retained so retries can manage the same roles. If the Job fails mid-provisioning,
those temporary permissions may remain until a successful retry or explicit
administrator cleanup. It does not drop data or rotate existing roles. It is NOT
part of the automatic deployment stages. On retry, first inspect the previous
Job's status. Never delete a running Job. After success, remove the completed
Job, its `rds-provision-admin` Secret and unused ServiceAccount, and remove the
local admin JSON. Keep the five private service credential files for retries.
Treat failure logs as potentially sensitive. Do not stream them into chat.

Provide the remaining Secrets through private files or the approved secret
manager, following docs/runbooks/kubernetes.md:

- `smtp`: SMTP_ADDR, SMTP_FROM, SMTP_USERNAME, SMTP_PASSWORD. Use SES port 587
  with STARTTLS. The Terraform SES identity/DKIM records must be published and
  verified. SES sandbox mode requires verified recipients; school policy may
  prevent production sending approval. Do not silently fall back to Mailpit.
- `jwt-signing`: `jwt-signing-private.pem`, generated privately and retained.
- `telegram`: TELEGRAM_BOT_TOKEN, empty to disable. Never run two pollers with
  the same bot token.
- `kafka-ui-auth`: SPRING_SECURITY_USER_NAME and SPRING_SECURITY_USER_PASSWORD.
- `foc-public-tls` in `istio-ingress`: a trusted certificate and private key for
  the hostname. DNS ownership and renewal are operator responsibilities.

Never put a password in a shell argument. Use `kubectl create secret ...
--from-env-file=<private-file>` or `--from-file` and avoid printing the manifest.
Do not configure static AWS credentials for Order; IRSA supplies credentials.
SES SMTP credentials are separate from Order's AWS role and must be obtained
securely. This package does not create an IAM user or SMTP access key.

## 5. Publish images and deploy

ECR repositories have immutable tags. Use a unique commit/build tag every time;
rebuilding an existing tag will fail. All eight images must target linux/amd64
because the worker nodes are x86_64, even when building on Apple Silicon.
Set `K8S_IMAGE_PREFIX` from deployment outputs:

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

The operator applies gateway, dependencies, initialization, then applications.
Its production provisioning stage is intentionally empty. It waits for migration
and Kafka initialization Jobs and rejects template origins/local image tags.
Terraform does not claim that successful resource creation means FoC works.

Point the owned hostname to the NLB using the authoritative DNS provider. For
Route 53 use an alias; for an external DNS provider use an appropriate CNAME
for the subdomain. Do not expose internal service ports or Kafka UI publicly.

## 6. Acceptance after deployment

Verify the exact public HTTPS URL with certificate validation, frontend loading,
all public health RPCs, real magic-link email/login, a signed image upload and
read-back, and a representative order flow supported by the current main branch.
Prove internal public-key and Credit reservation RPCs are not publicly exposed,
and test Order-to-Credit versus unauthorized in-cluster callers. Inspect actual
pod injection and storage persistence. Keep a repeatable script/report and any
UI evidence. Existing local-only smoke checks are not proof of external HTTPS.
The repository still explicitly does not claim the complete D3 demo workflow.

## 7. Teardown after seven days (separate approval)

Do not run `terraform destroy` first. The NLB and Kafka disk were created by
Kubernetes controllers and are not directly tracked by this Terraform stack.

1. Decide which database, images, objects, and Kafka data must be retained.
   Record snapshot IDs, bucket names, and retained disk IDs with their ongoing cost.
2. Delete the generated `foc-public` Gateway while its controller and cluster
   are still running. Wait until its generated Service, NLB, listeners, target
   groups, and controller-managed security groups are actually gone.
3. Delete Kafka's StatefulSet/PVC after the data decision. Its Retain policy
   leaves the PV/EBS disk. Explicitly preserve or delete that disk; deleting the
   EKS cluster does not dispose of it.
4. Empty only the intended ECR repositories and S3 order bucket if their data
   deletion was approved. Do not enable force deletion as a timeout workaround.
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
