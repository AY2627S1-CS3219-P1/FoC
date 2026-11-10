# CI prerequisites

This Terraform root owns only the private release-configuration bucket, GitHub OIDC deployment identity, EKS access entry, and a disposable runner launch template with its bootstrap identity and network rules. It uses a **different backend key** from the application infrastructure. Never point it at the application's state key. No EC2 instance runs merely because this root is applied.

Initialize the existing protected state bucket with native S3 locking, using a dedicated key such as `foc/ci/terraform.tfstate`. Supply `account_id`, `region`, `expires_on`, `cluster_name`, `private_subnet_id`, and a reviewed Amazon Linux 2023 x86_64 `runner_ami` through a private variable file. Review and fingerprint the saved plan before applying it. Retain the configuration bucket and its versions by default; its destroy protection is intentional. CI resources have the same expiry tag convention as the demo, not automatic teardown.

The deployment role trusts only GitHub's `sts.amazonaws.com` audience and `repo:AY2627S1-CS3219-P1@322586384/FoC@1378409897:environment:aws-demo` subject. GitHub issues immutable subjects for this repository, so the subject contains the owner and repository IDs. It can describe this cluster, push/read the nine ECR repositories, including CMS, retrieve versioned configuration, and create/delete its own environment-lock versions. It cannot read infrastructure state, administer IAM, provision infrastructure, or fetch database credentials from Secrets Manager. Kubernetes access is separately bound by `release-rbac.yaml`; apply it with an existing administrator, not the deployment role. Namespace-local deployment is privileged access to the application, including its workload Secrets and ServiceAccounts.

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

The runner has no public IP or inbound security-group rules. It reaches EKS over the existing private endpoint and uses the existing NAT gateway for outbound HTTPS and the signed Debian package downloads that the existing image builds fetch over HTTP. Its encrypted 80 GiB root disk is deleted on termination. IMDSv2 is required with hop limit one. CPU credits are Unlimited: a new T3 has no credits, and Standard mode throttled image builds to the 30% baseline. The runner terminates after one job, so surplus credits cost a few cents per release (about US$0.05 per vCPU-hour). No additional NAT or always-on runner is created.

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

The workflow also launches this runner itself for continuous deployment (below). Manual launches remain available for recovery.

## Continuous deployment

`aws-release.yml` runs after the **CI** workflow succeeds on a push to `main` and releases that exact commit. It has three jobs:

1. `launch-runner` runs on a GitHub-hosted runner in environment `aws-demo-runner`. It assumes the launcher role through OIDC and gets a short-lived GitHub App installation token. Then it runs `scripts/aws/launch_runner.py --keep-running`. The launcher role can only start instances from the runner launch template, pass the bootstrap role, write the one-time registration parameter, take the `runner-bootstrap` lock, and terminate instances tagged as the runner. It cannot deploy.
2. `release` runs on that private runner in environment `aws-demo`, using the existing deployment role and the configuration pinned by `AWS_CONFIG_VERSION` and `AWS_CONFIG_SHA256`.
3. `cleanup-runner` always runs after a successful launch. It terminates the runner instance and removes its GitHub registration if the runner did not already do so.

Workflow concurrency allows one release at a time. A newer pending run replaces an older pending run, and a running release is never cancelled. CI runs can finish out of push order, so an automatic run deploys only if its commit is still the head of `main`. It checks before launching the runner and again before deploying. A skipped older commit needs no action, because the newer commit releases after its own CI passes. A failed CI run, a pull request, a fork, or a branch other than `main` never starts a release. Missing configuration variables fail the release before any build. Terraform applies and `prepare-database` remain manual.

One-time administrator setup:

1. Apply this root to create the launcher role. Record `launcher_role_arn` from `terraform output -json ci`.
2. Create an organization GitHub App with **Organization → Self-hosted runners: Read and write** and no other permissions. Install it on the organization, selecting only FoC. Webhooks are not needed.
3. Create environment `aws-demo-runner` with `main` as its only deployment branch and no required reviewers. Add secret `RUNNER_APP_PRIVATE_KEY` (the App's private key). Add variables `RUNNER_APP_ID`, `AWS_LAUNCHER_ROLE_ARN`, `RUNNER_GROUP_ID`, `RUNNER_LAUNCH_TEMPLATE_ID`, `RUNNER_LAUNCH_TEMPLATE_VERSION`, `RUNNER_REGISTRATION_PARAMETER`, and `RUNNER_NAME` from the `ci` output and the runner group. Also add `AWS_ACCOUNT_ID`, `AWS_REGION`, `AWS_CONFIG_BUCKET`, and `AWS_CONFIG_PREFIX`.
4. In environment `aws-demo`, add variables `AWS_CONFIG_VERSION` and `AWS_CONFIG_SHA256` from the latest publication receipt. Update both after every infrastructure publication. The release never selects an unpinned latest object.
5. Remove the required reviewer from `aws-demo` after one automatic release has succeeded with the reviewer still in place. Keep `main` as its only deployment branch. While the reviewer remains, the runner is already running and its two-hour shutdown timer has started before approval. Approve within that window. Otherwise, reject the deployment: `cleanup-runner` then terminates the runner, and the next CI run releases again.

Update `RUNNER_LAUNCH_TEMPLATE_VERSION` whenever this root changes the launch template.

There is no standing autoscaler or manager. The GitHub App private key is the one long-lived GitHub credential. It lives only in the `aws-demo-runner` environment, and it can only manage self-hosted runners. Use the dedicated group only for trusted release code. Registering online proves bootstrap and private TLS reachability, not OIDC exchange, deployment authorization, image builds, migrations, or application release success.

## Verification

```sh
terraform -chdir=infra/aws/ci validate
terraform -chdir=infra/aws/ci test
.tools/aws-python/bin/python -m pip install -r scripts/aws/requirements-release-test.txt
PYTHONDONTWRITEBYTECODE=1 .tools/aws-python/bin/python -m unittest discover -s scripts/aws -p 'test_*.py' -v
```

Mock-provider assertions cover the environment-only OIDC trust, nine-repository/state-excluded deployment policy, encrypted disposable private runner, IMDSv2, Unlimited CPU credits, private/versioned bucket, and non-deploying bootstrap role. Offline CLI fixtures cover probe cleanup, expiring encrypted registration, refusal of unexpected workflow access or an existing instance, closed-group release rejection, and bootstrap failure. Live registration must be followed by read-back of runner-group/environment restrictions, IAM/EKS bindings, bucket settings, instance/profile/credit/network state, parameter deletion, and actual instance termination.

## Optional moderation image access

Set `enable_moderation_service = true` in this CI root only when preparing the
reports/appeals service. It adds the exact
`<cluster-name>/moderation-service` ECR ARN to the release role's existing image
permissions; the default remains the original nine repositories. The main AWS
root has a separate switch that creates the repository and isolated evidence
resources. Neither switch adds the service to release build/deploy inventories.
No additional Terraform-state, IAM-admin, evidence-bucket, or database-admin
access is granted to the release role.
