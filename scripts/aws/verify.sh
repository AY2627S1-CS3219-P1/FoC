#!/bin/sh
# Offline deployment-contract checks. No AWS credentials, apply, or live cluster needed.
set -eu
cd "$(dirname "$0")/../.."
TF_BIN=${TF_BIN:-terraform}

set -x
export AQUA_ROOT_DIR="$PWD/.tools/aqua"
export PATH="$PWD/.tools/bin:$AQUA_ROOT_DIR/bin:$PATH"
make k8s-tools aws-tools
"$TF_BIN" -chdir=infra/aws init -backend=false -input=false -lockfile=readonly
"$TF_BIN" -chdir=infra/aws fmt -check -recursive
"$TF_BIN" -chdir=infra/aws validate -no-color
"$TF_BIN" -chdir=infra/aws test -no-color
"$TF_BIN" -chdir=infra/aws/state init -input=false -lockfile=readonly
"$TF_BIN" -chdir=infra/aws/state validate -no-color
K8S_RENDER_TESTS=1 GOWORK=off go -C scripts test -mod=readonly ./aws -v
make fmt-check
(cd scripts && GOWORK=off go test -mod=readonly ./...)
set +x
printf '\nPASS: offline AWS configuration checks. No live deployment was tested.\n'
