# Supplier's old Location schema survives on existing Postgres volumes

**Applies when:** your `supplier_dev` database applied Supplier migrations before 8 October 2026. #118 edited the already-merged `00007_create_location_disablements.sql` and `00008_create_location_addition_requests.sql`.

**Symptom:** Supplier starts with `goose: no migrations to run. current version: 10`, but Location requests return 500 and the log shows:

```text
list current disablements: ERROR: column d.ended_at does not exist (SQLSTATE 42703)
```

To confirm, list the table's columns. The old schema has `ends_at` and `cancelled_at` but no `ended_at`, `updated_at` or `revision`:

```sh
docker compose exec postgres psql -U foc -d supplier_dev -c '\d location_disablements'
```

**Cause:** goose records each migration version once and never reruns it. A database that applied the original 00007 and 00008 keeps their schema after the files change.

**Current workaround:** recreate the Supplier database so every migration runs again. This deletes all local Supplier data:

```sh
docker compose stop supplier-service
docker compose exec postgres psql -U foc -c 'DROP DATABASE supplier_dev' -c 'CREATE DATABASE supplier_dev'
docker compose start supplier-service
```

Then reseed if you need demo locations. Use your `POSTGRES_USER` if it isn't `foc`.

**Remove this entry when:** no one still runs a `supplier_dev` database created before 8 October 2026, or a migration reconciles the old schema.
