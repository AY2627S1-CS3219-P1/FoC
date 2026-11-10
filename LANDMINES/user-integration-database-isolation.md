# User integration checks need a fresh database

**Applies when:** running User Service integration tests through `TEST_DATABASE_URL` more than once against a reused database, or running schema downgrade checks in a database containing additional test schemas.

**Reproduced:** 10 November 2026 while verifying the profile-picture stack. `internal/models/TestSchema` failed during `DownTo(0)` with `cannot drop extension citext because other objects depend on it` when another test schema retained citext columns. Separately, rerunning `internal/review/TestReviewRepositoryIntegration` against an already-used database failed its audit-row assertion because earlier `review_removals` rows remained. The same source passed in separate, freshly created databases.

The schema test removes database-wide extensions, so a separate schema in the same database does not isolate it. The review fixture also assumes its audit table starts empty; unique test-user IDs alone do not isolate that global assertion.

**Workaround:** give these checks fresh disposable databases. Run the schema/persistence check and the review check in different fresh databases, and recreate them before repeating the checks. Do not share the database with application data or other test schemas. A new database can share the same disposable PostgreSQL container.

**Tracking:** [issue #227](https://github.com/AY2627S1-CS3219-P1/FoC/issues/227) and [PR #255](https://github.com/AY2627S1-CS3219-P1/FoC/pull/255). The proposed per-test PostgreSQL containers in #255 remove the shared-fixture assumption; newly added integration tests must adopt the same isolation.

**Remove when:** all affected User integration fixtures use isolated databases, and running the migration and review checks twice no longer depends on manual database recreation. Verify newly added profile-picture integration tests when updating #255.
