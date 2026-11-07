#!/bin/sh
# Operator-only Job: create isolated service roles/databases without dropping data.
# The revocation store is one shared role/database every service connects to.
set -eu
export PGHOST=postgres PGPORT=5432 PGDATABASE=postgres PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD"
for service in user supplier order credit communication revocation; do
  DB_USER=$(cat "/secrets/$service/DB_USER")
  DB_PASSWORD=$(cat "/secrets/$service/DB_PASSWORD")
  DB_NAME=$(cat "/secrets/$service/DB_NAME")
  export DB_USER DB_PASSWORD DB_NAME
  psql -X --set ON_ERROR_STOP=1 <<'SQL'
\getenv db_user DB_USER
\getenv db_password DB_PASSWORD
\getenv db_name DB_NAME
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'db_user', :'db_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'db_user') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'db_name', :'db_user')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') \gexec
SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', :'db_name') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db_name', :'db_user') \gexec
SQL
done
# PostGIS requires a privileged install. The service migration remains the owner
# of its schema but its runtime/migration role never receives SUPERUSER.
PGDATABASE=$(cat /secrets/supplier/DB_NAME) psql -X --set ON_ERROR_STOP=1 -c 'CREATE EXTENSION IF NOT EXISTS postgis; CREATE EXTENSION IF NOT EXISTS pg_trgm;'

# The revocation store is shared: the writer role (revocation_app, used by the
# User Service) records revocations, and every other service reads through a
# SELECT-only role, so a compromised reader cannot forge or delete entries.
# The table is created at provision time so readers never need DDL privileges
# and can boot before the User Service.
READER_USER=$(cat "/secrets/revocation-reader/DB_USER")
READER_PASSWORD=$(cat "/secrets/revocation-reader/DB_PASSWORD")
DB_USER=$(cat "/secrets/revocation/DB_USER")
DB_NAME=$(cat "/secrets/revocation/DB_NAME")
export READER_USER READER_PASSWORD DB_USER DB_NAME
psql -X --set ON_ERROR_STOP=1 <<'SQL'
\getenv reader_user READER_USER
\getenv reader_password READER_PASSWORD
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'reader_user', :'reader_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'reader_user') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db_name', :'reader_user')
WHERE EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') \gexec
SQL
PGDATABASE="$DB_NAME" psql -X --set ON_ERROR_STOP=1 \
  --set db_user="$DB_USER" --set reader_user="$READER_USER" <<'SQL'
CREATE TABLE IF NOT EXISTS revoked_access_tokens (
    sid        TEXT PRIMARY KEY,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS revoked_access_tokens_expires_at_idx
    ON revoked_access_tokens (expires_at);
GRANT SELECT, INSERT, UPDATE, DELETE ON revoked_access_tokens TO :"db_user";
GRANT SELECT ON revoked_access_tokens TO :"reader_user";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO :"reader_user";
SQL
