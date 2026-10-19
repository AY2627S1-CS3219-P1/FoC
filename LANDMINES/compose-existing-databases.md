# An existing Compose volume can lack a newly added service database

**Applies when:** Compose reuses a PostgreSQL volume created before a service
database was added to `docker/postgres/init-dbs.sh` (for example, `order_dev`).

**Symptom:** PostgreSQL is healthy, but the new service exits with PostgreSQL
`FATAL: database "order_dev" does not exist` (`SQLSTATE 3D000`).

**Cause:** The PostgreSQL image intentionally runs entrypoint initialization
scripts only when its data directory is empty. Updating the mounted script does
not run it again on an existing volume. This was reproduced while checking the
additive Kubernetes deployment against an older Compose volume.

**Recovery:** Run the existing idempotent initialization script, then restart
only the failed service:

```sh
docker compose exec -T postgres /bin/bash /docker-entrypoint-initdb.d/init-dbs.sh
docker compose restart order-service
```

The script creates missing databases and leaves existing databases intact.
Do not use `docker compose down -v` to resolve this: that deletes saved data.
This is an operator upgrade step for persistent development data, not a change
to Compose's default startup behavior.

**Remove this entry when:** Compose no longer uses persistent PostgreSQL
entrypoint initialization, or a supported upgrade workflow ensures all service
databases exist before application startup.
