# User database keeps pre-UUID bigint IDs

**Applies when:** A user-service database was migrated before `e96ae74 refactor(user): use UUID model IDs`, or from a branch whose migrations still use `BIGSERIAL` IDs. Check with:

```sh
docker exec foc-postgres psql -U foc -d user_dev -tAc "SELECT data_type FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'id'"
```

It prints `bigint` instead of `uuid`.

**Symptom:** The service starts and logs `migrations applied`, but any insert fails. `RequestLink` and other RPCs return `internal`, and the log shows:

```text
sql: Scan error on column index 0, name "id": Scan: unable to scan type int64 into UUID
```

**Cause:** `e96ae74` changed migrations `00001`–`00005` in place. Goose records those versions as applied, so it never reruns them against an existing database.

**Current workaround:** Recreate the local user database. This deletes all local user-service data:

```sh
docker exec foc-postgres psql -U foc -d postgres -c "DROP DATABASE user_dev WITH (FORCE)" -c "CREATE DATABASE user_dev"
docker compose restart user-service
```

**Remove this entry when:** The user service rejects a pre-UUID schema at startup, or no team database predates `e96ae74`.
