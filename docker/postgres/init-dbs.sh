# Creates one database per service on the shared dev postgres server.
# Also provisions the dedicated revocation_dev database for the shared
# revocation store: the ${POSTGRES_USER} role is the writer (User Service),
# and every other service reads through the SELECT-only revocation_reader
# role, so a compromised reader cannot forge or delete entries.
set -e

REVOCATION_DB="${REVOCATION_DB:-revocation_dev}"
REVOCATION_READER="${REVOCATION_READER_ROLE:-revocation_reader}"
# Dev default; override REVOCATION_READER_PASSWORD in .env for anything else.
export REVOCATION_READER_PASSWORD="${REVOCATION_READER_PASSWORD:-revocation_reader_dev}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
  SELECT 'CREATE DATABASE "${USER_DB:-user_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${USER_DB:-user_dev}')\gexec
  SELECT 'CREATE DATABASE "${SUPPLIER_DB:-supplier_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${SUPPLIER_DB:-supplier_dev}')\gexec
  SELECT 'CREATE DATABASE "${ORDER_DB:-order_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${ORDER_DB:-order_dev}')\gexec
  SELECT 'CREATE DATABASE "${CREDIT_DB:-credit_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${CREDIT_DB:-credit_dev}')\gexec
  SELECT 'CREATE DATABASE "${COMMUNICATION_DB:-communication_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${COMMUNICATION_DB:-communication_dev}')\gexec
  SELECT 'CREATE DATABASE "${REVOCATION_DB}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${REVOCATION_DB}')\gexec
  \getenv reader_pw REVOCATION_READER_PASSWORD
  SELECT format('CREATE ROLE ${REVOCATION_READER} LOGIN PASSWORD %L', :'reader_pw') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${REVOCATION_READER}')\gexec
  SELECT 'GRANT CONNECT ON DATABASE ${REVOCATION_DB} TO ${REVOCATION_READER}' WHERE EXISTS (SELECT FROM pg_roles WHERE rolname = '${REVOCATION_READER}')\gexec
EOSQL

# The revocation table is created at init time so reader services never need
# DDL privileges and can boot before the User Service.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$REVOCATION_DB" <<-EOSQL
  CREATE TABLE IF NOT EXISTS revoked_access_tokens (
      sid        TEXT PRIMARY KEY,
      revoked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      expires_at TIMESTAMPTZ NOT NULL
  );
  CREATE INDEX IF NOT EXISTS revoked_access_tokens_expires_at_idx
      ON revoked_access_tokens (expires_at);
  GRANT SELECT, INSERT, UPDATE, DELETE ON revoked_access_tokens TO "${POSTGRES_USER}";
  GRANT SELECT ON revoked_access_tokens TO ${REVOCATION_READER};
  ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO ${REVOCATION_READER};
EOSQL
