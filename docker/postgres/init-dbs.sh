#!/bin/bash
# Creates one database per service on the shared dev postgres server.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
  SELECT 'CREATE DATABASE "${USER_DB:-user_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${USER_DB:-user_dev}')\gexec
  SELECT 'CREATE DATABASE "${SUPPLIER_DB:-supplier_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${SUPPLIER_DB:-supplier_dev}')\gexec
EOSQL
