#!/bin/sh
# Offline deployment-contract checks. No AWS credentials, apply, or live cluster needed.
set -eu
cd "$(dirname "$0")/../.."
TF_BIN=${TF_BIN:-terraform}
PYTHON=${PYTHON:-python3}

if [ ! -x .tools/aws-python/bin/python ]; then
  "$PYTHON" -m venv .tools/aws-python
fi
set -x
.tools/aws-python/bin/python -m pip install -r scripts/aws/requirements-test.txt
"$TF_BIN" -chdir=infra/aws init -backend=false -input=false -lockfile=readonly
"$TF_BIN" -chdir=infra/aws fmt -check -recursive
"$TF_BIN" -chdir=infra/aws validate -no-color
"$TF_BIN" -chdir=infra/aws test -no-color
"$TF_BIN" -chdir=infra/aws/state init -input=false -lockfile=readonly
"$TF_BIN" -chdir=infra/aws/state validate -no-color
.tools/aws-python/bin/python -m unittest discover -s scripts/aws -p 'test_*.py' -v
make fmt-check
(cd scripts && GOWORK=off go test -mod=readonly ./...)
set +x
printf '\nPASS: offline AWS configuration checks. No live deployment was tested.\n'
