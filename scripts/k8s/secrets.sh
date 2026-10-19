#!/usr/bin/env bash
# Preserve local credentials across retries; never rotate an established DB password.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require kubectl openssl
umask 077
prepare_state
secret_dir="$K8S_STATE_DIR/secrets"
mkdir -p "$secret_dir"
chmod 700 "$K8S_STATE_DIR" "$secret_dir"
secret_lock="$secret_dir/.generating"
mkdir "$secret_lock" 2>/dev/null || die "Another secret generator holds $secret_lock; inspect before removing a stale lock."
trap 'rmdir "$secret_lock"' EXIT

persist_value() {
    local name=$1 value=$2
    if [[ -e $secret_dir/$name ]]; then
        [[ -s $secret_dir/$name ]] || die "Empty credential $secret_dir/$name; restore it rather than rotating a database password."
    else
        printf '%s' "$value" > "$secret_dir/$name"
    fi
}

for name in postgres-password user-password supplier-password order-password kafka-ui-password; do
    persist_value "$name" "$(openssl rand -hex 32)"
done
persist_value postgres-user foc
persist_value kafka-ui-user operator
persist_value SMTP_ADDR mailpit:1025
persist_value SMTP_FROM 'FoC <no-reply@foc.local>'
# Empty SMTP authentication is intentional for local Mailpit.
touch "$secret_dir/SMTP_USERNAME" "$secret_dir/SMTP_PASSWORD"

if [[ ! -e $secret_dir/jwt-signing-private.pem ]]; then
    openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$secret_lock/jwt.pem"
    mv "$secret_lock/jwt.pem" "$secret_dir/jwt-signing-private.pem"
fi
openssl pkey -in "$secret_dir/jwt-signing-private.pem" -check -noout >/dev/null
if [[ ! -e $secret_dir/tls.key && ! -e $secret_dir/tls.crt ]]; then
    openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 365 \
        -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
        -keyout "$secret_lock/tls.key" -out "$secret_lock/tls.crt" 2>/dev/null
    mv "$secret_lock/tls.key" "$secret_dir/tls.key"
    mv "$secret_lock/tls.crt" "$secret_dir/tls.crt"
fi
[[ -s $secret_dir/tls.key && -s $secret_dir/tls.crt ]] || die 'Incomplete local TLS keypair; restore its matching files.'
openssl x509 -in "$secret_dir/tls.crt" -noout -checkend 0 >/dev/null || die 'Local TLS certificate expired; explicitly renew the matching certificate and key.'
cert_public=$(openssl x509 -in "$secret_dir/tls.crt" -pubkey -noout)
key_public=$(openssl pkey -in "$secret_dir/tls.key" -pubout)
[[ $cert_public = "$key_public" ]] || die 'Local TLS certificate and key do not match.'

apply_secret() {
    local namespace=$1 name=$2
    shift 2
    k -n "$namespace" create secret generic "$name" "$@" --dry-run=client -o yaml | k apply -f -
}
apply_secret foc postgres-admin \
    --from-file="POSTGRES_USER=$secret_dir/postgres-user" --from-file="POSTGRES_PASSWORD=$secret_dir/postgres-password"
for service in user supplier order; do
    persist_value "$service-db-user" "${service}_app"
    persist_value "$service-db-name" "${service}_db"
    # Generated passwords are hex, so no URL escaping is needed.
    persist_value "$service-database-url" "postgres://$(< "$secret_dir/$service-db-user"):$(< "$secret_dir/$service-password")@postgres:5432/$(< "$secret_dir/$service-db-name")?sslmode=disable"
    apply_secret foc "$service-db" \
        --from-file="DB_USER=$secret_dir/$service-db-user" --from-file="DB_NAME=$secret_dir/$service-db-name" \
        --from-file="DB_PASSWORD=$secret_dir/$service-password" --from-file="DATABASE_URL=$secret_dir/$service-database-url"
done
apply_secret foc jwt-signing --from-file="jwt-signing-private.pem=$secret_dir/jwt-signing-private.pem"
apply_secret foc smtp --from-file="$secret_dir/SMTP_ADDR" --from-file="$secret_dir/SMTP_FROM" \
    --from-file="$secret_dir/SMTP_USERNAME" --from-file="$secret_dir/SMTP_PASSWORD"
apply_secret foc kafka-ui-auth --from-file="SPRING_SECURITY_USER_NAME=$secret_dir/kafka-ui-user" \
    --from-file="SPRING_SECURITY_USER_PASSWORD=$secret_dir/kafka-ui-password"
k -n istio-ingress create secret tls foc-local-tls --cert="$secret_dir/tls.crt" --key="$secret_dir/tls.key" \
    --dry-run=client -o yaml | k apply -f -
note "Local secrets preserved in $secret_dir. Trust tls.crt explicitly for https://localhost:8443."
