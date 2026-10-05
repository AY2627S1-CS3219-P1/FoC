# Locking a row does not refresh relationship subqueries

**Applies when:** a PostgreSQL `READ COMMITTED` transaction uses `SELECT ... FOR UPDATE` with a subquery that reads related rows, and another transaction can change those relationships while holding the parent row lock.

**Symptom:** the locking query returns updated parent fields but old relationship values after waiting for the other transaction. Approval can then create a Location with obsolete Categories and overwrite the request's current Category links.

**Reproduced in:** `TestOperationalWorkflowsPostGIS/approval_waiting_behind_a_Category_edit_uses_committed_Category_links` in supplier-service/internal/rpc/location/lifecycle/workflow_integration_test.go. The test holds the request lock, changes its Category links, starts approval, observes its lock wait, and commits the edit. Before the fix, approval returned both original Categories instead of the one committed Category.

**Cause:** PostgreSQL can recheck the updated locked row without refreshing the statement snapshot used by relationship subqueries.

**Fix:** lock the parent first. Read its relationships in a separate statement after the locking query completes. Hold the parent lock through relationship reads and writes. The workflow repository follows this sequence with `LockWorkflowRequest` and `WorkflowRequestCategoryIDs`.

**Verification:** from supplier-service/, run:

```sh
go test -race -tags=integration ./internal/rpc/location/lifecycle -run 'TestOperationalWorkflowsPostGIS/approval_waiting' -count=1 -timeout=10m
```

**Remove this entry when:** relationship reads no longer rely on this locking and isolation pattern, or a shared enforced query boundary prevents relationship subqueries in locking reads.
