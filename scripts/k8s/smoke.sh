#!/usr/bin/env bash
# Non-destructive infrastructure checks. kubectl access is trusted operator access.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require kubectl curl
check_context
prepare_state
umask 077
smoke_tmp=$(mktemp -d "$K8S_STATE_DIR/smoke.XXXXXX")
suffix="$$-$RANDOM"
outsider_ns="foc-untrusted-$suffix"
client="foc-smoke-$suffix"
pf_pid=
mail_pid=
cleanup() {
    [[ -z $pf_pid ]] || kill "$pf_pid" 2>/dev/null || true
    [[ -z $mail_pid ]] || kill "$mail_pid" 2>/dev/null || true
    k -n foc delete pod "$client" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    k -n istio-ingress delete pod "$client" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    k delete namespace "$outsider_ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT
pass() { note "PASS $*"; }
fail() { die "FAIL $* (evidence: $smoke_tmp)"; }

make_client() {
    local namespace=$1 name=$2 account=$3 injected=$4
    k apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $name
  namespace: $namespace
  labels:
    app: foc-smoke
    sidecar.istio.io/inject: "$injected"
  annotations:
    sidecar.istio.io/nativeSidecar: "true"
spec:
  serviceAccountName: $account
  restartPolicy: Never
  activeDeadlineSeconds: 600
  containers:
    - name: client
      image: ${K8S_SMOKE_IMAGE:-curlimages/curl:8.12.1}
      command: ["sleep", "600"]
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
        limits:
          memory: 64Mi
EOF
    k -n "$namespace" wait --for=condition=Ready "pod/$name" --timeout="$K8S_TIMEOUT"
}

assert_rpc() {
    local namespace=$1 pod=$2 url=$3 expected=$4 description=$5 response actual
    if ! response=$(k -n "$namespace" exec "$pod" -c client -- curl -sS --max-time 15 \
        -w $'\n%{http_code}' -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}' "$url"); then
        fail "$description: expected HTTP $expected, transport failed"
    fi
    actual=${response##*$'\n'}
    [[ $actual = "$expected" ]] || fail "$description: expected HTTP $expected, got $actual: $response"
    if [[ $expected = 200 && $url = *GetPublicKeys ]]; then
        [[ $response = *ES256* && $response = *keys* ]] || fail "$description: missing ES256 keys"
    fi
    pass "$description (expected=$expected actual=$actual)"
}

assert_direct_blocked() {
    local namespace=$1 pod=$2 url=$3 description=$4 code result
    if result=$(k -n "$namespace" exec "$pod" -c client -- curl -sS --max-time 10 \
        -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{}' "$url" 2> "$smoke_tmp/direct-$namespace-$pod.txt"); then
        [[ $result = 403 ]] || fail "$description: expected mesh denial, got HTTP $result"
        code=0
    else
        code=$?
        case $code in 35|52|56) ;; *) fail "$description: expected TLS rejection/reset, got curl exit $code" ;; esac
    fi
    pass "$description (HTTP=${result:-000} curl=$code); direct pod IP is not a supported discovery URL"
}

start_forward() {
    local namespace=$1 service=$2 local_port=$3 remote_port=$4 logfile=$5
    local args=(--context "$K8S_CONTEXT")
    [[ $K8S_EXISTING != 0 ]] || args+=(--kubeconfig "$K8S_STATE_DIR/clusters/$K8S_CLUSTER.config")
    kubectl "${args[@]}" -n "$namespace" port-forward --address 127.0.0.1 "service/$service" "$local_port:$remote_port" > "$logfile" 2>&1 &
    forward_pid=$!
    local deadline=$((SECONDS + 30))
    while (( SECONDS < deadline )); do
        if grep -q 'Forwarding from' "$logfile"; then return 0; fi
        kill -0 "$forward_pid" 2>/dev/null || { cat "$logfile" >&2; fail "Port forward $local_port failed"; }
        sleep 1
    done
    kill "$forward_pid" 2>/dev/null || true
    fail "Port forward $local_port did not start"
}

for app in "${K8S_APPS[@]}"; do
    k -n foc rollout status "deployment/$app" --timeout="$K8S_TIMEOUT"
done
for dependency in postgres kafka; do
    k -n foc rollout status "statefulset/$dependency" --timeout="$K8S_TIMEOUT"
done
for job in db-provision "${K8S_JOBS[@]}"; do wait_job "$job"; done
[[ $(k -n foc get peerauthentication strict -o jsonpath='{.spec.mtls.mode}') = STRICT ]] || fail 'STRICT mTLS policy missing'
pass 'Application, dependency and initialization readiness; STRICT mTLS declared'

k create namespace "$outsider_ns" --dry-run=client -o yaml | k apply -f -
# The outsider namespace deliberately has no foc.dev/mesh admission label.
make_client foc "$client" default true
make_client istio-ingress "$client" istio-ingressgateway true
make_client "$outsider_ns" meshed default true
make_client "$outsider_ns" plaintext default false
key_path=/user.v1.PublicKeyService/GetPublicKeys
internal="http://user-service-internal.foc.svc.cluster.local:8080$key_path"
assert_rpc foc "$client" "$internal" 200 'Authenticated app identity reaches internal key RPC'
assert_rpc foc "$client" 'http://user-service-internal.foc.svc.cluster.local:8080/user.v1.HealthService/Check' 200 'Public health uses the same internal destination URL'
assert_rpc istio-ingress "$client" "$internal" 403 'Gateway identity denied internal RPC'
assert_rpc "$outsider_ns" meshed "$internal" 403 'Foreign mesh namespace denied internal RPC'
user_pod=$(k -n foc get pod -l app=user-service -o jsonpath='{.items[0].metadata.name}')
user_ip=$(k -n foc get pod "$user_pod" -o jsonpath='{.status.podIP}')
assert_direct_blocked foc "$client" "http://$user_ip:8081$key_path" 'Raw pod-IP traffic cannot bypass protected listener'
assert_direct_blocked "$outsider_ns" meshed "http://$user_ip:8081$key_path" 'Foreign raw pod-IP traffic denied'
if k -n "$outsider_ns" exec plaintext -c client -- curl -fsS --max-time 10 \
    -H 'Content-Type: application/json' -d '{}' "$internal" > "$smoke_tmp/plaintext.txt" 2>&1; then
    fail 'Plaintext pod unexpectedly reached a protected listener'
fi
# A failed request alone is not proof of mTLS. Inspect the destination's real config.
k -n foc exec "$user_pod" -c istio-proxy -- pilot-agent request GET config_dump > "$smoke_tmp/user-proxy.json"
grep -Eq '"require_client_certificate"[[:space:]]*:[[:space:]]*true|"requireClientCertificate"[[:space:]]*:[[:space:]]*true' "$smoke_tmp/user-proxy.json" || fail 'Proxy config does not require client certificates'
pass 'Plaintext rejected; destination proxy config requires client certificates'

tls_cert=${K8S_TLS_CA_FILE:-$K8S_STATE_DIR/secrets/tls.crt}
[[ -s $tls_cert ]] || fail 'Set K8S_TLS_CA_FILE to the gateway trust certificate'
https_port=${K8S_SMOKE_HTTPS_PORT:-18443}
start_forward istio-ingress istio-ingressgateway "$https_port" 443 "$smoke_tmp/gateway-forward.log"
pf_pid=$forward_pid
base="https://localhost:$https_port"
http_code=$(curl --cacert "$tls_cert" -sS --max-time 20 -o "$smoke_tmp/frontend.html" -w '%{http_code}' "$base/")
[[ $http_code = 200 ]] || fail "Frontend HTTPS expected 200, got $http_code"
for namespace in user supplier order; do
    http_code=$(curl --cacert "$tls_cert" -sS --max-time 20 -o "$smoke_tmp/$namespace-health.json" -w '%{http_code}' \
        -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}' "$base/$namespace.v1.HealthService/Check")
    [[ $http_code = 200 ]] || fail "$namespace public health expected 200, got $http_code"
done
http_code=$(curl --cacert "$tls_cert" -sS --max-time 20 -o "$smoke_tmp/public-key-response.txt" -w '%{http_code}' \
    -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}' "$base$key_path")
[[ $http_code = 404 ]] || fail "Internal procedure via public gateway expected 404, got $http_code"
grep -qi '<html' "$smoke_tmp/public-key-response.txt" && fail 'Internal RPC returned frontend HTML' || true
pass 'Trusted HTTPS frontend and public health; internal procedure absent publicly (expected=404 actual=404)'

if [[ -n ${K8S_BOOTSTRAP_SUPERADMIN_EMAIL:-} ]]; then
    require node
    [[ -f $K8S_ROOT/scripts/k8s/auth-smoke.mjs ]] || fail 'Auth smoke runner missing'
    mail_port=${K8S_SMOKE_MAILPIT_PORT:-19025}
    start_forward foc mailpit "$mail_port" 8025 "$smoke_tmp/mailpit-forward.log"
    mail_pid=$forward_pid
    NODE_EXTRA_CA_CERTS="$tls_cert" SMOKE_USER_ORIGIN="$base" SMOKE_ORIGIN=https://localhost:8443 \
        SMOKE_MAILPIT_URL="http://127.0.0.1:$mail_port" \
        node "$K8S_ROOT/scripts/k8s/auth-smoke.mjs"
else
    note 'SKIP browser auth: set K8S_BOOTSTRAP_SUPERADMIN_EMAIL during bootstrap and smoke to test magic-link/login/refresh/logout.'
fi
pass "Infrastructure smoke complete; evidence $smoke_tmp. Credit/business workflow remains unverified."
