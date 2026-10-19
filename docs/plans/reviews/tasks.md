# Reviews feature — implementation tasks

Implement the review feature for user-service per `plans/spec(user-service): add review feature.md`
and the accepted ADRs. The whole subdomain stack lives in `user-service/internal/review/`
(domain model, repository interface, service logic, sqlc repository, Connect handler), following
`iz/feat/add-admin-allowed-domain-mgt`.

## 1. Migration `user-service/migrations/00006_review.sql`
- Add `review_count INTEGER NOT NULL DEFAULT 0` and `avg_rating DOUBLE PRECISION NOT NULL DEFAULT 0` to `users`.
- Create `reviews` (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at).
- Create `review_removals` (id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by).
- Unique indexes `UNIQUE (reviewer_id, order_id)` and `UNIQUE (reviewee_id, order_id)` on reviews.
- Index on `reviews(reviewee_id)` for indexed COUNT/list.
- FKs: reviewer_id/reviewee_id -> users(id); review_removals.created_by -> users(id); order_id no FK.
- IDs keep `DEFAULT gen_random_uuid()` (yihao's uuidv7 migration deferred); IDs are generated in Go.
- goose Up/Down.

## 2. sqlc setup
- `user-service/database/sqlc.yaml`: add a `review` entry (engine postgresql, schema `../migrations`,
  package `reviewdb`, out `../internal/database/reviewdb`, sql_package `database/sql`, citext override).
- `user-service/database/query/reviews.sql`: InsertReview, FindReview, DeleteReview,
  InsertReviewRemoval, ListReviewsForUser, ListReviewsFromUser, CountReviewsForUser,
  CountReviewsFromUser, ListReviewRemovals, UserExists, LockReviewee, RevieweeStatistics, UpdateRevieweeStats.
- Run `sqlc generate` (`tools/sqlc/1.30.0/sqlc`) to produce `internal/database/reviewdb/`.

## 1. Migration `user-service/migrations/00006_review.sql` ✅
## 2. sqlc setup ✅
## 3. Domain package `user-service/internal/review/` ✅ model/repository/service/postgres_repository
- [x] `authorization.go`: `AuthorizeInterceptor()` (RBAC switch, token-claims, no DB lookup) + `authorizeCaller`.
- [x] `handler.go`: Connect handler; derives reviewer from caller, `from == caller` impersonation check,
  ABAC suspended self-target for ListReviewsForUser (small private method owned by the handler), pagination mapping.
- [x] Tests: `service_test.go`, `handler_test.go` (unit, fakes), `authorization_test.go` (RBAC matrix: suspended-submit, admin-only remove/audit, unprocedured list), `postgres_repository_integration_test.go` (`-tags=integration`).
## 4. Wire it up ✅ main.go/router.go/Makefile
## 5. Verify
## 6. Commit in logical chunks (conventional commits)
## 7. Draft PR against main with a concise but complete description.

## 4. Wire it up
- `user-service/cmd/server/main.go`: build `reviewdb.New(sqlDB)`, review repo, service + `withTransaction`
  (bind `reviewdb.New(tx.Statement.ConnPool)`), construct handler, add to `ProtectedRoutes.Review`.
- `user-service/internal/router/router.go`: mount `ReviewService` with `validate + review.AuthorizeInterceptor + normalizeRPCError`
  (bypasses DB-resolved `authorizeProtected`).
- `user-service/Makefile`: add `sqlc` target mirroring reference.

## 5. Verify
- `make sqlc` regenerates cleanly.
- `go build ./...`, `go vet ./...`, `gofmt -l`.
- Unit tests: `go test ./...`. Integration tests with a throwaway DB (`-tags=integration`) if `TEST_DATABASE_URL` reachable.
- Confirm recalculation convergence and unique-index enforcement via integration test.

## 6. Commit in logical chunks (conventional commits)
- proto/generated, migration+sqlc, review package, wiring, tests — separate commits.

## 7. Draft PR against main with a concise but complete description.
