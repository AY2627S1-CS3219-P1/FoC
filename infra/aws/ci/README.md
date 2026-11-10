# CI prerequisites

This Terraform root owns only the private release-configuration bucket, GitHub OIDC deployment identity, EKS access entry, and a disposable runner launch template with its bootstrap identity and network rules. It uses a **different backend key** from the application infrastructure. Never point it at the application's state key. No EC2 instance runs merely because this root is applied.

Initialize the existing protected state bucket with native S3 locking, using a dedicated key such as `foc/ci/terraform.tfstate`. Supply `account_id`, `region`, `expires_on`, `cluster_name`, `private_subnet_id`, and a reviewed Amazon Linux 2023 x86_64 `runner_ami` through a private variable file. Review and fingerprint the saved plan before applying it. Retain the configuration bucket and its versions by default; its destroy protection is intentional. CI resources have the same expiry tag convention as the demo, not automatic teardown.

The deployment role trusts only GitHub's `sts.amazonaws.com` audience and `repo:AY2627S1-CS3219-P1/FoC:environment:aws-demo` subject. It can describe this cluster, push/read the nine ECR repositories, including CMS, retrieve versioned configuration, and create/delete its own environment-lock versions. It cannot read infrastructure state, administer IAM, provision infrastructure, or fetch database credentials from Secrets Manager. Kubernetes access is separately bound by `release-rbac.yaml`; apply it with an existing administrator, not the deployment role. Namespace-local deployment is privileged access to the application, including its workload Secrets and ServiceAccounts.

Logging additionally requires the namespace-local `foc-release` Role/Binding in
`foc-observability`. It manages Grafana/Alloy and Helm-owned Loki resources and
Helm's dynamic release-history Secrets. Those Secret permissions also cover the
existing Grafana credential Secret; use this role only for trusted release code.
It cannot administer RBAC, namespaces, CRDs or other namespaces' Secrets, and it
cannot delete logging PVCs. An administrator must first install Alloy's
`infra/k8s/base/observability-rbac` and provide `grafana-admin` privately.

## GitHub administrator setup

Create a protected `aws-demo` environment with `main` as its only deployment branch and a required reviewer. Set `AWS_ACCOUNT_ID`, `AWS_REGION`, `AWS_RELEASE_ROLE_ARN`, `AWS_CONFIG_BUCKET`, and `AWS_CONFIG_PREFIX` from `terraform output -json ci` as environment variables, not secrets. Do not add static AWS keys.

Create organization runner group `foc-release`, selecting **only FoC**, allowing its public repository, and enabling `restricted_to_workflows`. Authorize only `AY2627S1-CS3219-P1/FoC/.github/workflows/aws-release.yml@refs/heads/main`. GitHub validates that the workflow exists at that ref. Before merge, retain an empty `selected_workflows` array so the group permits **no jobs**; do not substitute a PR ref or another workflow to make registration succeed. The release workflow explicitly selects this group. Leave the organization's default group and other repositories unchanged.

## Disposable runner

The runner has no public IP or inbound security-group rules. It reaches EKS over the existing private endpoint and uses the existing NAT gateway for outbound HTTPS and the signed Debian package downloads that the existing image builds fetch over HTTP. Its encrypted 80 GiB root disk is deleted on termination. IMDSv2 is required with hop limit one, and CPU credits are Standard. No additional NAT or always-on runner is created.

Bootstrap installs Docker/Buildx, the Amazon Linux `awscli-2` package, and the GitHub runner, verifies the pinned download checksums, probes the private EKS endpoint with its actual CA, then consumes a one-job JIT registration from an encrypted Advanced SSM parameter. The JIT blob can exceed the standard parameter's 4 KiB limit. Its parameter has a one-hour expiration policy and is deleted after consumption. The bootstrap identity can read/delete only that parameter and decrypt only its SSM encryption context; it cannot deploy. The operator detaches this profile once the runner is online. Workflow AWS authentication uses OIDC.

Run with an approved infrastructure identity and an existing organization-admin `gh` login. The launcher uses that login only to create/delete one-job registrations; it does not copy the GitHub token to AWS or the VM. Never print the JIT configuration or place it in user data, the configuration bucket, or workflow artifacts.

```sh
umask 077
mkdir -p .local/aws/ci
terraform -chdir=infra/aws/ci output -json ci > .local/aws/ci/ci.json

# Before activation, with an empty workflow allowlist: probe, then terminate/remove it.
python3 scripts/aws/launch_runner.py \
  --configuration .local/aws/ci/ci.json --runner-group "$RUNNER_GROUP_ID"

# After the exact main workflow is permitted, launch before an approved dispatch.
python3 scripts/aws/launch_runner.py \
  --configuration .local/aws/ci/ci.json --runner-group "$RUNNER_GROUP_ID" \
  --keep-running
```

The registration probe requires a closed group; it refuses an active workflow allowlist rather than risk claiming a queued release. An operator-only conditional S3 launch lock serializes creation through parameter cleanup. A stale launch lock is never automatically stolen; inspect its exact version and owner before recovery.

A retained runner waits for one job, then shuts down and terminates. Bootstrap failures also shut it down. A two-hour **guest-side** shutdown timer bounds an ordinary unattended instance, but it is not a billing cap or an external guarantee against a hung guest. An operator must verify termination and remove any unused GitHub registration. The launcher records the instance/registration IDs in its private owner receipt and requests cleanup after failed probes. It refuses a second runner while an earlier instance remains pending, running, stopping, stopped, or shutting down. A killed launcher or interrupted API response still requires inspecting actual AWS/GitHub state; do not blindly retry.

Bootstrap emits fixed stage names and exit codes to the EC2 serial console, without shell tracing or registration material. A failed package installation also emits its final package-manager lines before registration is fetched. The launcher saves console output in its private launch directory on failure before cleanup deletes the disk. It prefers buffered state-transition output and falls back to a live snapshot if buffering is delayed; AWS console snapshots can be incomplete. Inspect that protected output to distinguish installation, downloads, private TLS, and registration failures; do not publish raw diagnostics.

This is an operator-launched ephemeral runner, not an automatic autoscaler. There is no standing manager or long-lived GitHub application credential. Use the dedicated group only for trusted release code. Registering online proves bootstrap and private TLS reachability, not OIDC exchange, deployment authorization, image builds, migrations, or application release success.

## Verification

```sh
terraform -chdir=infra/aws/ci validate
terraform -chdir=infra/aws/ci test
.tools/aws-python/bin/python -m pip install -r scripts/aws/requirements-release-test.txt
PYTHONDONTWRITEBYTECODE=1 .tools/aws-python/bin/python -m unittest discover -s scripts/aws -p 'test_*.py' -v
```

Mock-provider assertions cover the environment-only OIDC trust, nine-repository/state-excluded deployment policy, encrypted disposable private runner, IMDSv2, Standard CPU credits, private/versioned bucket, and non-deploying bootstrap role. Offline CLI fixtures cover probe cleanup, expiring encrypted registration, refusal of unexpected workflow access or an existing instance, closed-group release rejection, and bootstrap failure. Live registration must be followed by read-back of runner-group/environment restrictions, IAM/EKS bindings, bucket settings, instance/profile/credit/network state, parameter deletion, and actual instance termination.
