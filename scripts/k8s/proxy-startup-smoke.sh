#!/usr/bin/env bash
# Negative test in an isolated namespace: a broken proxy cannot start the app.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require kubectl
check_context
test_namespace="foc-proxy-startup-$$-$RANDOM"
cleanup() { k delete namespace "$test_namespace" --ignore-not-found --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT
k create namespace "$test_namespace"
# This test namespace deliberately does NOT carry foc.dev/mesh=required. The
# production admission policy forbids this broken-proxy override altogether.
k apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: blocked-app
  namespace: $test_namespace
  labels:
    sidecar.istio.io/inject: "true"
  annotations:
    sidecar.istio.io/nativeSidecar: "true"
    proxy.istio.io/config: '{"discoveryAddress":"127.0.0.1:1"}'
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 150
  containers:
    - name: application
      image: curlimages/curl:8.12.1
      command: ["sleep", "120"]
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
EOF
deadline=$((SECONDS + 130))
while (( SECONDS < deadline )); do
    state=$(k -n "$test_namespace" get pod blocked-app -o jsonpath='{.status.containerStatuses[?(@.name=="application")].containerID}:{.status.initContainerStatuses[?(@.name=="istio-proxy")].restartCount}')
    [[ $state = :* ]] || die 'FAIL: application started despite an unavailable proxy.'
    restarts=${state#*:}
    if [[ $restarts =~ ^[1-9][0-9]*$ ]]; then
        note 'PASS: unavailable Istio proxy exhausted its startup probe and restarted; application never started.'
        exit 0
    fi
    sleep 2
done
k -n "$test_namespace" get pod blocked-app >&2
die 'FAIL: did not observe a bounded proxy startup failure; application has not started.'
