# Supplier operational test fixtures omit required review revisions

**Applies when:** running Supplier's broad operational RPC/PostGIS integration
suite (`tests/location/rpc`, `TestOperationalWorkflowsPostGIS`).

**Symptom:** approval, rejection, shared-hours approval, rollback, and approval
lock tests fail with `invalid_argument`; the lock test never reaches the row lock.

**Reproduced:** on unchanged commit `598141c8` using a clean git archive and
disposable PostGIS 18/3.6 on 10 November 2026:

```sh
cd supplier-service
GOWORK=off go test -mod=readonly -tags=integration ./tests/location/rpc \
  -run 'TestOperationalWorkflowsPostGIS/approval_atomically' -count=1
```

**Cause:** older workflow test callers omit `expected_revision` when approving
or rejecting proposals. The contract and domain now require a positive revision,
so these requests fail before reaching the behavior under test. Production
revision checks must remain strict.

**Tracking:** [Issue #344](https://github.com/AY2627S1-CS3219-P1/FoC/issues/344).
The proximity discovery tests and regular Supplier tests pass independently.

**Workaround:** report this baseline failure separately when verifying unrelated
changes. Use focused discovery tests for proximity SQL; do not weaken review
validation or claim the broad workflow suite passes.

**Remove when:** fixtures supply the revision of the reviewed proposal and the
full operational suite plus stale-review/replay tests pass. Close #344 then.
