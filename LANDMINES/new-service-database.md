# A new service's database is missing on existing Postgres volumes

**Applies when:** a service database is added to `docker/postgres/init-dbs.sh` (such as `communication_dev`) and your `pgdata` volume was created before that change.

**Symptom:** the new service exits at startup:

```text
FATAL: database "communication_dev" does not exist
```

**Cause:** Postgres runs `/docker-entrypoint-initdb.d` scripts only when it initializes an empty data directory. An existing `pgdata` volume never reruns `init-dbs.sh`, so databases added to it later are not created.

**Current workaround:** run the existing idempotent initialization script to create missing service databases, then restart only the affected service:

```sh
docker compose exec -T postgres /bin/bash /docker-entrypoint-initdb.d/init-dbs.sh
docker compose restart communication-service
```

The script uses the container configuration and leaves existing databases intact.
Do not reset the volume with `docker compose down -v`: it deletes every service's local data. This also applies to older volumes lacking `order_dev` or `credit_dev`.

**Remove this entry when:** service databases are created on startup instead of by the init script, or no new service databases are expected.
