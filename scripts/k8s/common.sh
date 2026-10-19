#!/usr/bin/env bash
# Shared operator configuration. Every Kubernetes command names its context.
set -euo pipefail

K8S_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
K8S_TOOLS_DIR=${K8S_TOOLS_DIR:-$K8S_ROOT/.tools/k8s}
export PATH="$K8S_TOOLS_DIR:$PATH"
K8S_STATE_DIR=${K8S_STATE_DIR:-$K8S_ROOT/.local/k8s}
K8S_CLUSTER=${K8S_CLUSTER:-foc}
K8S_EXISTING=0
[[ -z ${K8S_CONTEXT:-} ]] || K8S_EXISTING=1
K8S_CONTEXT=${K8S_CONTEXT:-kind-$K8S_CLUSTER}
K8S_OVERLAY_EXPLICIT=${K8S_OVERLAY:+1}
K8S_OVERLAY=${K8S_OVERLAY:-$K8S_ROOT/infra/k8s/overlays/local}
[[ $K8S_OVERLAY = /* ]] || K8S_OVERLAY="$K8S_ROOT/$K8S_OVERLAY"
K8S_NAMESPACE=foc
K8S_TIMEOUT=${K8S_TIMEOUT:-600s}
K8S_IMAGE_TAG=${K8S_IMAGE_TAG:-local}
K8S_IMAGE_PREFIX=${K8S_IMAGE_PREFIX:-foc}
K8S_ISTIO_VERSION=1.31.2
K8S_NODE_IMAGE=${K8S_NODE_IMAGE:-kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f}
K8S_APPS=(user-service supplier-service order-service frontend)
K8S_JOBS=(user-migrate supplier-migrate order-migrate kafka-init)

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
note() { printf '%s\n' "$*" >&2; }

require() {
    local tool
    for tool in "$@"; do
        command -v "$tool" >/dev/null || die "Missing $tool; run make k8s-tools (Docker and OpenSSL are host prerequisites)."
    done
}

prepare_state() {
    if ! mkdir -p "$K8S_STATE_DIR/clusters"; then
        die "Cannot create $K8S_STATE_DIR. If Compose created a root-owned .local, create only this directory with: sudo install -d -o $(id -u) -g $(id -g) -m 700 '$K8S_STATE_DIR'; or set K8S_STATE_DIR to a writable private directory."
    fi
    chmod 700 "$K8S_STATE_DIR" "$K8S_STATE_DIR/clusters"
}

k() {
    if [[ $K8S_EXISTING = 0 ]]; then
        kubectl --kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config" --context "$K8S_CONTEXT" "$@"
    else
        kubectl --context "$K8S_CONTEXT" "$@"
    fi
}

h() {
    if [[ $K8S_EXISTING = 0 ]]; then
        helm --kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config" --kube-context "$K8S_CONTEXT" "$@"
    else
        helm --kube-context "$K8S_CONTEXT" "$@"
    fi
}

check_context() {
    require kubectl
    [[ $(k config get-contexts "$K8S_CONTEXT" -o name) = "$K8S_CONTEXT" ]] || die "Context $K8S_CONTEXT does not exist. Run make k8s-up for the local cluster."
    k cluster-info >/dev/null
}

check_overlay() {
    if [[ $K8S_EXISTING = 1 && -z $K8S_OVERLAY_EXPLICIT ]]; then
        die 'An existing cluster requires explicit K8S_CONTEXT and K8S_OVERLAY.'
    fi
    local stage
    for stage in dependencies provisioning initialization applications; do
        [[ -f $K8S_OVERLAY/$stage/kustomization.yaml ]] || die "Missing $K8S_OVERLAY/$stage/kustomization.yaml"
    done
}

is_local_overlay() {
    [[ $(cd -- "$K8S_OVERLAY" && pwd -P) = "$K8S_ROOT/infra/k8s/overlays/local" ]]
}

validate_service() {
    case "$1" in user-service|supplier-service|order-service|frontend) ;;
        *) die "Unknown service '$1'; use user-service, supplier-service, order-service or frontend." ;;
    esac
}

image_for() { printf '%s/%s:%s' "$K8S_IMAGE_PREFIX" "$1" "$K8S_IMAGE_TAG"; }

wait_job() {
    local name=$1 result deadline=$((SECONDS + ${K8S_JOB_TIMEOUT_SECONDS:-600}))
    while (( SECONDS < deadline )); do
        result=$(k -n foc get job "$name" -o jsonpath='{.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}')
        case "$result" in
            [1-9]*:*) return 0 ;;
            *:True) k -n foc logs "job/$name" --all-containers --tail=100 >&2 || true; die "Job $name failed; fix the cause before retrying." ;;
        esac
        sleep 2
    done
    k -n foc describe job "$name" >&2 || true
    die "Timed out waiting for Job $name. Existing running Jobs are preserved."
}

# Only these exact, terminal Jobs can be replaced. Never interrupt running work.
prepare_job() {
    local name=$1 result
    result=$(k -n foc get job "$name" --ignore-not-found -o jsonpath='{.metadata.name}:{.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}')
    [[ -n $result ]] || return 0
    case "$result" in
        "$name":[1-9]*:*|"$name":*:True) k -n foc delete job "$name" --wait=true --timeout="$K8S_TIMEOUT" ;;
        *) die "Job $name is not terminal; inspect or wait for it before retrying. It has not been deleted." ;;
    esac
}
