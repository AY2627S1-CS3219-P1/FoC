#!/bin/sh
set -eu

if [ "${APP_ENV:-}" = local ]; then
	key="${JWT_PRIVATE_KEY_FILE:-/app/.local/secrets/auth/jwt-signing-private.pem}"
	export JWT_PRIVATE_KEY_FILE="$key"
	mkdir -p "$(dirname "$key")"
	if [ ! -e "$key" ]; then
		tmp="${key}.tmp.$$"
		trap 'rm -f "$tmp"' EXIT HUP INT TERM
		(umask 077 && openssl ecparam -name prime256v1 -genkey -noout -out "$tmp")
		ln "$tmp" "$key" 2>/dev/null || [ -e "$key" ]
		rm -f "$tmp"
		trap - EXIT HUP INT TERM
	fi
fi

goose -dir database/schema postgres "$DATABASE_URL" up
exec air -c .air.toml
