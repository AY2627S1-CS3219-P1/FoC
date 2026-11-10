#!/bin/bash
# Creates one database per service on the shared dev postgres server.
# Idempotent: Compose's postgres-init reruns it against existing volumes.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
  -- Content moderation used moderation_dev before reports/appeals needed it; keep its data.
  SELECT EXISTS (SELECT FROM pg_database WHERE datname = 'moderation_dev') AND NOT EXISTS (SELECT FROM pg_database WHERE datname = '${CONTENT_MODERATION_DB:-content_moderation_dev}') AS rename_content_moderation \gset
  \if :rename_content_moderation
  SELECT count(*) FILTER (WHERE pg_terminate_backend(pid, 5000)) AS terminated_sessions FROM pg_stat_activity WHERE datname = 'moderation_dev' \gset
  ALTER DATABASE moderation_dev RENAME TO "${CONTENT_MODERATION_DB:-content_moderation_dev}";
  \endif
  SELECT 'CREATE DATABASE "${USER_DB:-user_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${USER_DB:-user_dev}')\gexec
  SELECT 'CREATE DATABASE "${SUPPLIER_DB:-supplier_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${SUPPLIER_DB:-supplier_dev}')\gexec
  SELECT 'CREATE DATABASE "${ORDER_DB:-order_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${ORDER_DB:-order_dev}')\gexec
  SELECT 'CREATE DATABASE "${CREDIT_DB:-credit_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${CREDIT_DB:-credit_dev}')\gexec
  SELECT 'CREATE DATABASE "${COMMUNICATION_DB:-communication_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${COMMUNICATION_DB:-communication_dev}')\gexec
  SELECT 'CREATE DATABASE "${CONTENT_MODERATION_DB:-content_moderation_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${CONTENT_MODERATION_DB:-content_moderation_dev}')\gexec
  SELECT 'CREATE DATABASE "${MODERATION_DB:-moderation_dev}"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${MODERATION_DB:-moderation_dev}')\gexec
EOSQL
