# AWS demo read-only Terraform checks need Linux provider hashes

## Applies when

Running the AWS demo's read-only Terraform verifier on Linux amd64.

## Reproduced failures

- With Terraform 1.13.5 on Linux amd64, main-stack initialization using
  `-lockfile=readonly` succeeds with a lockfile warning, then validation rejects
  all five unpacked providers for checksum mismatches. A normal initialization
  in a disposable checkout adds exactly five authenticated `h1` content hashes,
  without changing provider versions or archive hashes; validation and all six
  mocked tests then pass. The state bootstrap already has the Linux AWS content
  hash and validates successfully.

This failure was reproduced during PR #292's rebase onto main `0ae181b3`.
The logging integration is now covered by a regression invoking the actual
operator and, with `K8S_RENDER_TESTS=1`, the pinned Helm chart.

## Proceed

Track the remaining checksum fix in [issue #317](https://github.com/AY2627S1-CS3219-P1/FoC/issues/317).

Populate authenticated checksums for supported Terraform
platforms with `terraform providers lock`, then retain read-only initialization
in the verifier. Perform any diagnostic lockfile updates in an isolated checkout
and review them before committing; do not silently change deployment providers.

## Removal condition

Verify that a fresh Linux checkout passes the read-only Terraform verifier
without changing the tracked lockfile. Update or remove this entry and close
#317 when the checksum fix lands.
