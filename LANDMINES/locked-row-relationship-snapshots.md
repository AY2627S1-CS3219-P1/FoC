# Locking a row does not refresh relationship subqueries

**Applies when:** a PostgreSQL `READ COMMITTED` transaction uses `SELECT ... FOR UPDATE` with a subquery that reads related rows, and another transaction can change those relationships while holding the parent row lock.

**Symptom:** the locking query returns updated parent fields but old relationship values after waiting for the other transaction. Approval can then create a Location with obsolete Categories and overwrite the request's current Category links.

**Reproduced in:** `TestWorkflowPersistencePostGIS/Location links are read after a row lock wait` in supplier-service/internal/location/repository_integration_test.go. The test changes a Location and its Category links while holding the row lock, starts a Location read, observes its lock wait, and commits the edit. It verifies that the returned Location and Category links use the committed values.

**Cause:** PostgreSQL can recheck the updated locked row without refreshing the statement snapshot used by relationship subqueries.

**Fix:** lock the parent first. Read its relationships in a separate statement after the locking query completes. Hold the parent lock through relationship reads and writes. The workflow repository follows this sequence with `LockWorkflowRequest` and `WorkflowRequestCategoryIDs`.

**Verification:** from supplier-service/, run:

```sh
GOWORK=off go test -p 1 -race -tags=integration ./internal/location -run 'TestWorkflowPersistencePostGIS/Location links are read after a row lock wait' -count=1 -timeout=10m
```

**Remove this entry when:** relationship reads no longer rely on this locking and isolation pattern, or a shared enforced query boundary prevents relationship subqueries in locking reads.
