#!/usr/bin/env bash
# Explicit, opt-in fixtures for the local Compose databases. No tokens/passwords are seeded.
set -euo pipefail

reset_demo=false
case "${1:-}" in
  '') ;;
  --reset) reset_demo=true ;;
  *) echo "Usage: $0 [--reset]" >&2; exit 2 ;;
esac
if (( $# > 1 )); then echo "Usage: $0 [--reset]" >&2; exit 2; fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

# Check both schemas before writing either database. Database names come from
# the container's configured environment, rather than hard-coded host credentials.
for database in USER_DB SUPPLIER_DB; do
  if ! docker compose exec -T postgres sh -eu -c '
    case "$1" in USER_DB) database_name=$USER_DB ;; SUPPLIER_DB) database_name=$SUPPLIER_DB ;; *) exit 2 ;; esac
    case "$1" in USER_DB) relation=role_changes ;; SUPPLIER_DB) relation=supplier_idempotency ;; esac
    psql -X -U "$POSTGRES_USER" -d "$database_name" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM $relation LIMIT 0"
  ' sh "$database"; then
    echo 'Schemas are not ready. Start Compose and wait for both services to finish migrations, then rerun.' >&2
    exit 1
  fi
done

seed_database() {
  local database_variable=$1 fixture_file=$2
  docker compose exec -T postgres sh -eu -c '
    case "$1" in USER_DB) database_name=$USER_DB ;; SUPPLIER_DB) database_name=$SUPPLIER_DB ;; *) exit 2 ;; esac
    exec psql -X -q -U "$POSTGRES_USER" -d "$database_name" -v ON_ERROR_STOP=1 -v reset_demo="$2"
  ' sh "$database_variable" "$reset_demo" < "$fixture_file"
}

seed_database USER_DB data/demo/users.sql
seed_database SUPPLIER_DB data/demo/suppliers.sql
printf '\nDemo data ready. Sign in at http://localhost:5173/login; read links in Mailpit at http://localhost:8025.\n'
printf 'Accounts and test scenarios: data/demo/README.md\n'
