#!/usr/bin/env bash
# Shell units/static checks; no Docker daemon or Kubernetes API is contacted.
set -euo pipefail
test_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
for script in "$test_root"/scripts/k8s/*.sh; do bash -n "$script"; done
source "$test_root/scripts/k8s/common.sh"
pass() { printf 'PASS %s\n' "$*"; }
expect_failure() {
    local description=$1
    shift
    if ( "$@" ) >/dev/null 2>&1; then die "Expected failure: $description"; fi
    pass "$description"
}

validate_service user-service
validate_service supplier-service
validate_service order-service
validate_service frontend
expect_failure 'Unknown service rejected' validate_service credit-service
[[ $(image_for user-service) = "$K8S_IMAGE_PREFIX/user-service:$K8S_IMAGE_TAG" ]]
pass 'Image references use the configured prefix/tag'

(
    K8S_EXISTING=1
    K8S_OVERLAY_EXPLICIT=
    expect_failure 'Existing-cluster mutations require an explicit overlay' check_overlay
)
(
    K8S_EXISTING=1
    K8S_CONTEXT=production-review
    kubectl() { printf '%s\n' "$@"; }
    [[ $(k get pods) = $'--context\nproduction-review\nget\npods' ]]
    pass 'Existing-cluster kubectl always names context'
)
(
    K8S_EXISTING=0
    K8S_CONTEXT=kind-foc
    K8S_STATE_DIR=/private/foc-test
    kubectl() { printf '%s\n' "$@"; }
    [[ $(k get pods) = $'--kubeconfig\n/private/foc-test/clusters/foc.config\n--context\nkind-foc\nget\npods' ]]
    pass 'Local kubectl uses a private kubeconfig'
)

job_calls=
k() {
    if [[ $3 = get ]]; then
        printf '%s' "$job_result"
    elif [[ $3 = delete ]]; then
        [[ $4 = job && $5 = user-migrate ]] || die 'Unexpected deletion target'
        job_calls=deleted
    else
        die "Unexpected mocked kubectl operation: $*"
    fi
}
job_result=
prepare_job user-migrate
[[ -z $job_calls ]]
pass 'Missing Job requires no deletion'
job_result=user-migrate:1:
prepare_job user-migrate
[[ $job_calls = deleted ]]
pass 'Only an exact completed Job can be replaced'
job_calls=
job_result=user-migrate::True
prepare_job user-migrate
[[ $job_calls = deleted ]]
pass 'Terminal failed Job can be retried'
job_result=user-migrate::
expect_failure 'Running Job is preserved' prepare_job user-migrate
job_result=1:
wait_job user-migrate
pass 'Completed Job wait succeeds'

expect_failure 'D3 demo cannot falsely claim complete coverage' bash "$test_root/scripts/k8s/k8s.sh" demo
expect_failure 'Deleting an external cluster is rejected' env K8S_CONTEXT=external bash "$test_root/scripts/k8s/k8s.sh" delete-cluster
expect_failure 'Local deletion requires the exact cluster name' env -u K8S_CONTEXT -u K8S_CONFIRM_DELETE bash "$test_root/scripts/k8s/k8s.sh" delete-cluster

if [[ -x $K8S_TOOLS_DIR/kubectl ]]; then
    for stage in dependencies provisioning initialization applications; do
        output=$(env -u K8S_CONTEXT -u K8S_OVERLAY K8S_IMAGE_PREFIX=registry.example/foc K8S_IMAGE_TAG=unit-test \
            bash "$test_root/scripts/k8s/k8s.sh" render "$stage")
        if [[ $stage = initialization || $stage = applications ]]; then
            [[ $output != *'image: foc/'* ]] || die "Untransformed image in $stage"
            [[ $output = *'image: registry.example/foc/user-service:unit-test'* ]] || die "Missing User image in $stage"
        fi
    done
    pass 'All four stages render; image overrides include migration Jobs'
else
    printf 'SKIP stage renders: make k8s-tools installs kubectl\n'
fi
if command -v shellcheck >/dev/null; then
    shellcheck -x -e SC1091 "$test_root"/scripts/k8s/*.sh
    pass 'ShellCheck'
fi
if [[ -f $test_root/scripts/k8s/auth-smoke.mjs ]] && command -v node >/dev/null; then
    node --check "$test_root/scripts/k8s/auth-smoke.mjs"
fi
pass 'Shell syntax and operator safety checks'
