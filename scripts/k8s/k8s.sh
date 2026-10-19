#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"

create_local_cluster() {
    require kind
    [[ $K8S_CLUSTER =~ ^[a-z0-9][a-z0-9-]*$ ]] || die 'K8S_CLUSTER must be a lowercase kind cluster name.'
    prepare_state
    local cluster found=0
    while IFS= read -r cluster; do
        [[ $cluster != "$K8S_CLUSTER" ]] || found=1
    done < <(kind get clusters)
    if [[ $found = 0 ]]; then
        kind create cluster --name "$K8S_CLUSTER" --image "$K8S_NODE_IMAGE" \
            --kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config" --wait "$K8S_TIMEOUT"
    else
        # Export into our private file. The user's kubeconfig is never merged or edited.
        kind export kubeconfig --name "$K8S_CLUSTER" --kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config"
    fi
}

install_mesh() {
    require helm
    local charts="$K8S_TOOLS_DIR/istio-$K8S_ISTIO_VERSION/manifests/charts"
    [[ -d $charts/base ]] || die 'Pinned Istio charts missing; run make k8s-tools.'
    k apply -f "$K8S_ROOT/infra/k8s/platform/namespaces.yaml"
    k apply -f "$K8S_ROOT/infra/k8s/platform/admission.yaml"
    h upgrade --install istio-base "$charts/base" -n istio-system --wait --timeout "$K8S_TIMEOUT"
    h upgrade --install istiod "$charts/istio-control/istio-discovery" -n istio-system \
        -f "$K8S_ROOT/infra/k8s/platform/istiod-values.yaml" --wait --timeout "$K8S_TIMEOUT"
    h upgrade --install istio-ingressgateway "$charts/gateway" -n istio-ingress \
        -f "$K8S_ROOT/infra/k8s/platform/gateway-values.yaml" --wait --timeout "$K8S_TIMEOUT"
}

build_image() {
    local name=$1 dockerfile=service
    case "$name" in frontend|kafka-init) dockerfile=$name ;; esac
    docker build --file "$K8S_ROOT/docker/k8s/$dockerfile.Dockerfile" \
        --build-arg "SERVICE=$name" --tag "$(image_for "$name")" "$K8S_ROOT"
    if [[ $K8S_EXISTING = 0 ]]; then
        kind load docker-image --name "$K8S_CLUSTER" "$(image_for "$name")"
    else
        [[ ${K8S_PUSH_IMAGES:-0} = 1 ]] || die 'Existing cluster: set K8S_IMAGE_PREFIX to a registry and K8S_PUSH_IMAGES=1, or K8S_SKIP_BUILD=1 for already published images.'
        docker push "$(image_for "$name")"
    fi
}

build_images() {
    [[ ${K8S_SKIP_BUILD:-0} != 1 ]] || return 0
    require docker
    if [[ $K8S_EXISTING = 1 && ${K8S_PUSH_IMAGES:-0} != 1 ]]; then
        die 'Existing cluster image builds require K8S_PUSH_IMAGES=1 and a registry K8S_IMAGE_PREFIX; otherwise use K8S_SKIP_BUILD=1.'
    fi
    local name
    for name in "$@"; do build_image "$name"; done
}

render_stage() (
    local stage=$1
    local render_tmp name
    [[ $K8S_IMAGE_PREFIX =~ ^[a-zA-Z0-9._:/-]+$ && $K8S_IMAGE_TAG =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$ ]] || die 'Invalid image prefix or tag.'
    render_tmp=$(mktemp -d)
    trap 'rm -rf -- "$render_tmp"' EXIT
    k kustomize "$K8S_OVERLAY/$stage" > "$render_tmp/resources.yaml"
    {
        printf 'apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - resources.yaml\nimages:\n'
        for name in "${K8S_APPS[@]}" kafka-init; do
            printf '  - name: foc/%s\n    newName: %s/%s\n    newTag: "%s"\n' "$name" "$K8S_IMAGE_PREFIX" "$name" "$K8S_IMAGE_TAG"
        done
    } > "$render_tmp/kustomization.yaml"
    k kustomize "$render_tmp"
)

apply_stage() { render_stage "$1" | k apply -f -; }

patch_bootstrap() {
    [[ -n ${K8S_BOOTSTRAP_SUPERADMIN_EMAIL:-} ]] || return 0
    k -n foc patch configmap foc-config --type merge --patch-file <(
        k -n foc create configmap foc-config --dry-run=client -o json \
            --from-literal="BOOTSTRAP_SUPERADMIN_EMAIL=$K8S_BOOTSTRAP_SUPERADMIN_EMAIL" \
            --from-literal="BOOTSTRAP_SUPERADMIN_DISPLAY_NAME=${K8S_BOOTSTRAP_SUPERADMIN_DISPLAY_NAME:-Admin}"
    )
}

deploy_all() {
    check_overlay
    check_context
    if ! is_local_overlay; then
        local dependency_config
        dependency_config=$(k kustomize "$K8S_OVERLAY/dependencies")
        if grep -Eq 'example\.invalid|REPLACE_ME' <<< "$dependency_config"; then
            die 'Production overlay is a template: configure a real hostname/origin and external Secrets first (docs/runbooks/kubernetes.md).'
        fi
        [[ $K8S_IMAGE_PREFIX != foc && $K8S_IMAGE_TAG != local ]] || die 'Production deployment requires a registry K8S_IMAGE_PREFIX and an immutable K8S_IMAGE_TAG.'
    fi
    build_images "${K8S_APPS[@]}" kafka-init
    if is_local_overlay; then
        bash "$K8S_ROOT/scripts/k8s/secrets.sh"
    else
        local secret
        for secret in postgres-admin user-db supplier-db order-db jwt-signing smtp kafka-ui-auth; do
            k -n foc get secret "$secret" >/dev/null
        done
        note 'Using operator-provided configuration and Secrets; see the production overlay runbook.'
    fi
    apply_stage dependencies
    patch_bootstrap
    k -n foc rollout status statefulset/postgres --timeout="$K8S_TIMEOUT"
    k -n foc rollout status statefulset/kafka --timeout="$K8S_TIMEOUT"
    k -n foc rollout status deployment/kafka-ui --timeout="$K8S_TIMEOUT"
    if is_local_overlay; then k -n foc rollout status deployment/mailpit --timeout="$K8S_TIMEOUT"; fi
    prepare_job db-provision
    apply_stage provisioning
    wait_job db-provision
    local job
    for job in "${K8S_JOBS[@]}"; do prepare_job "$job"; done
    apply_stage initialization
    for job in "${K8S_JOBS[@]}"; do wait_job "$job"; done
    apply_stage applications
    # Local tags are deliberately reusable during development. Restart so a rebuild is used.
    local app
    for app in "${K8S_APPS[@]}"; do
        k -n foc rollout restart "deployment/$app"
        k -n foc rollout status "deployment/$app" --timeout="$K8S_TIMEOUT"
    done
    note 'Bootstrap coverage: User, Supplier, Order, frontend, PostgreSQL, Kafka, Kafka UI; local overlay also Mailpit. Credit and the complete D3 business workflow are absent.'
}

deploy_one() {
    local service=$1
    validate_service "$service"
    check_overlay
    check_context
    build_images "$service"
    # Existing resources only: do not create other applications or rerun migration Jobs.
    k -n foc set image "deployment/$service" "$service=$(image_for "$service")"
    k -n foc rollout restart "deployment/$service"
    k -n foc rollout status "deployment/$service" --timeout="$K8S_TIMEOUT"
    note 'Application updated. Schema changes require a full make k8s-deploy first.'
}

forward() {
    local namespace=$1 service=$2 local_port=$3 remote_port=$4 scheme=$5
    check_context
    note "$scheme://localhost:$local_port (foreground; Ctrl-C stops access)."
    k -n "$namespace" port-forward --address 127.0.0.1 "service/$service" "$local_port:$remote_port"
}

status() {
    check_context
    k -n foc get pods,jobs,pvc,services -o wide
    k -n istio-system get pods
    k -n istio-ingress get pods,services
    k -n foc get events --field-selector type=Warning --sort-by=.metadata.creationTimestamp
    if command -v istioctl >/dev/null; then
        if [[ $K8S_EXISTING = 0 ]]; then
            istioctl --kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config" --context "$K8S_CONTEXT" proxy-status
        else
            istioctl --context "$K8S_CONTEXT" proxy-status
        fi
    fi
}

delete_cluster() {
    [[ $K8S_EXISTING = 0 ]] || die 'Cluster deletion is local-only. Unset K8S_CONTEXT and select K8S_CLUSTER explicitly.'
    [[ ${K8S_CONFIRM_DELETE:-} = "$K8S_CLUSTER" ]] || die "This deletes cluster $K8S_CLUSTER and its PostgreSQL/Kafka PVC data. Rerun with K8S_CONFIRM_DELETE=$K8S_CLUSTER. Local credential files are retained."
    [[ $K8S_CLUSTER =~ ^[a-z0-9][a-z0-9-]*$ ]] || die 'Invalid kind cluster name.'
    require kind
    kind delete cluster --name "$K8S_CLUSTER"
    note "Deleted local cluster $K8S_CLUSTER and cluster data. Credentials remain in $K8S_STATE_DIR."
}

case ${1:-help} in
    render)
        check_overlay
        case ${2:-} in dependencies|provisioning|initialization|applications) render_stage "$2" ;; *) die 'render requires a stage name.' ;; esac
        ;;
    up)
        check_overlay
        bash "$K8S_ROOT/scripts/k8s/tools.sh"
        if [[ $K8S_EXISTING = 0 ]]; then create_local_cluster; fi
        check_context
        install_mesh
        deploy_all
        ;;
    deploy) if [[ -n ${2:-} ]]; then deploy_one "$2"; else deploy_all; fi ;;
    access) forward istio-ingress istio-ingressgateway "${K8S_HTTPS_PORT:-8443}" 443 https ;;
    kafka-ui) forward foc kafka-ui "${K8S_KAFKA_UI_PORT:-18080}" 8080 http ;;
    mailpit) forward foc mailpit "${K8S_MAILPIT_PORT:-18025}" 8025 http ;;
    status) status ;;
    smoke) bash "$K8S_ROOT/scripts/k8s/smoke.sh" ;;
    demo) die 'D3 demo is unavailable: Credit and the integrated registration/credit/order workflow are not implemented. Use make k8s-smoke for infrastructure checks.' ;;
    delete-cluster) delete_cluster ;;
    *) note 'Usage: k8s.sh up|deploy [service]|access|status|smoke|demo|delete-cluster|kafka-ui|mailpit'; exit 2 ;;
esac
