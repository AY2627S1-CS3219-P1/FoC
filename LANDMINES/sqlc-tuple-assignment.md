# sqlc rejects tuple assignments with named parameters

**Applies when:** sqlc v1.30.0 generates a PostgreSQL update using
`SET (a, b) = (SELECT ...)` with named parameters inside the subquery.

**Symptom:** `sqlc generate` fails with `2 edits overlap`, even though
PostgreSQL accepts the equivalent parameterized SQL.

**Reproduced in:** the review-stat recalculation refactor on PR #155, and an
isolated sqlc v1.30.0 configuration using `database/sql` generation with this
schema and query:

```sql
CREATE TABLE users (id uuid PRIMARY KEY, review_count integer, avg_rating double precision);
CREATE TABLE reviews (reviewee_id uuid, rating integer);
```

```sql
-- name: UpdateStats :exec
UPDATE users
SET (review_count, avg_rating) = (
    SELECT COUNT(*)::integer, COALESCE(AVG(rating), 0)::double precision
    FROM reviews WHERE reviewee_id = @reviewee_id::uuid
)
WHERE id = @reviewee_id::uuid;
```

The same update was successfully prepared on PostgreSQL 18 with `$1` replacing
`@reviewee_id`. The failure is in sqlc generation, rather than SQL validity.

**Resolution implemented:** use `UPDATE ... FROM (SELECT ...) AS stats` with
individual assignments from the aggregate columns. The review queries use
this form; it generates successfully with the same named parameter reused in
the aggregate and target-row filter. No sqlc upgrade is required for this
workaround.

**Remove this entry when:** the project's sqlc version generates the reproducer
successfully and tuple assignments no longer require this workaround.
