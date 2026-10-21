# A new service's database is missing on existing Postgres volumes

**Applies when:** a service database is added to `docker/postgres/init-dbs.sh` (such as `communication_dev`) and your `pgdata` volume was created before that change.

**Symptom:** the new service exits at startup:

```text
FATAL: database "communication_dev" does not exist
```

**Cause:** Postgres runs `/docker-entrypoint-initdb.d` scripts only when it initializes an empty data directory. An existing `pgdata` volume never reruns `init-dbs.sh`, so databases added to it later are not created.

**Current workaround:** create the database once in the running Postgres container:

```sh
docker compose exec postgres psql -U foc -c 'CREATE DATABASE communication_dev'
```

Use your `POSTGRES_USER` if it isn't `foc`. Resetting with `docker compose down -v` also works, but it deletes every service's local data.

**Remove this entry when:** service databases are created on startup instead of by the init script, or no new service databases are expected.
