# AWS demo checks and deployment need current-main integration

## Applies when

Validating or deploying the AWS demo from `infra/aws` after integrating main's
mandatory Kubernetes logging stack, or running its read-only Terraform verifier
on Linux amd64.

## Reproduced failures

- The AWS renderer generates five stages, while the operator requires six.
  Rendering a gateway through the actual Go operator rejects the generated
  overlay because `observability/kustomization.yaml` is missing. Adding an empty
  stage only exposes the next missing prerequisite: the four `K8S_LOKI_*`
  production settings. The AWS package also lacks dedicated Loki bucket/identity
  wiring and the required `foc-observability/grafana-admin` Secret instructions.
- With Terraform 1.13.5 on Linux amd64, main-stack initialization using
  `-lockfile=readonly` succeeds with a lockfile warning, then validation rejects
  all five unpacked providers for checksum mismatches. A normal initialization
  in a disposable checkout adds exactly five authenticated `h1` content hashes,
  without changing provider versions or archive hashes; validation and all six
  mocked tests then pass. The state bootstrap already has the Linux AWS content
  hash and validates successfully.

These failures were reproduced during PR #292's rebase onto main `0ae181b3`,
including the logging stack from PR #261. Existing Go and Python fixture tests
pass without detecting the missing stage.

## Proceed

Track both fixes in [issue #317](https://github.com/AY2627S1-CS3219-P1/FoC/issues/317).
Do not treat the existing fixture checks as proof that the AWS deployment path
works. Do not bypass mandatory logging, reuse Order's bucket/identity for Loki,
or apply infrastructure as a verification workaround.

Complete the logging stage, its dedicated resource/input/Secret wiring, and the
cost/teardown guidance. Populate authenticated checksums for supported Terraform
platforms with `terraform providers lock`, then retain read-only initialization
in the verifier. Perform any diagnostic lockfile updates in an isolated checkout
and review them before committing; do not silently change deployment providers.

## Removal condition

Verify that a fresh Linux checkout passes the read-only Terraform verifier and
that the actual operator accepts the generated AWS overlay with all logging
prerequisites documented and wired. Update or remove this entry and close #317
when those fixes land.
