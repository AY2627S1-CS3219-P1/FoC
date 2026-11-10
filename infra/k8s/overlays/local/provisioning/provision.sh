#!/bin/sh
# Operator-only Job: create isolated service roles/databases without dropping data.
set -eu
export PGHOST=postgres PGPORT=5432 PGDATABASE=postgres PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD"
# Content moderation used the bare moderation names before reports/appeals needed them.
# Rename its original database and login in place so its data is kept.
DB_USER=$(cat /secrets/content-moderation/DB_USER)
DB_PASSWORD=$(cat /secrets/content-moderation/DB_PASSWORD)
DB_NAME=$(cat /secrets/content-moderation/DB_NAME)
export DB_USER DB_PASSWORD DB_NAME
psql -X --set ON_ERROR_STOP=1 <<'SQL'
\getenv db_user DB_USER
\getenv db_password DB_PASSWORD
\getenv db_name DB_NAME
SELECT EXISTS (SELECT FROM pg_database WHERE datname = 'moderation_db' AND pg_get_userbyid(datdba) = 'moderation_app')
  AND NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') AS rename_database \gset
\if :rename_database
SELECT count(*) FILTER (WHERE pg_terminate_backend(pid, 5000)) AS terminated_sessions
FROM pg_stat_activity WHERE datname = 'moderation_db' \gset
SELECT format('ALTER DATABASE moderation_db RENAME TO %I', :'db_name') \gexec
\endif
SELECT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name' AND pg_get_userbyid(datdba) = 'moderation_app')
  AND NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'db_user') AS rename_role \gset
\if :rename_role
SELECT format('ALTER ROLE moderation_app RENAME TO %I', :'db_user') \gexec
SELECT format('ALTER ROLE %I PASSWORD %L', :'db_user', :'db_password') \gexec
\endif
SQL
for service in user supplier order credit communication content-moderation moderation; do
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
