#!/usr/bin/env bash
# Each service owns its fixtures and persistence. Compose only orchestrates them.
set -euo pipefail

user_args=()
supplier_args=(--demo-dir /data/demo)
case "${1:-}" in
  '') ;;
  --reset) user_args+=(--reset); supplier_args+=(--reset-demo) ;;
  *) echo "Usage: $0 [--reset]" >&2; exit 2 ;;
esac
if (( $# > 1 )); then echo "Usage: $0 [--reset]" >&2; exit 2; fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

docker compose exec -T user-service go run ./cmd/seed "${user_args[@]}"
docker compose exec -T supplier-service go run ./cmd/seed "${supplier_args[@]}"
printf '\nDemo data ready. Sign in at http://localhost:5173/login; read links in Mailpit at http://localhost:8025.\n'
printf 'Accounts and test scenarios: data/demo/README.md\n'
