# Plan: review removal as soft delete

## Context / Why
Currently `RemoveReview` **hard‑deletes** the `reviews` row and records a
`ReviewRemoval` audit row. The product wants reviews removable without
physically deleting the review: soft‑deleted reviews must be invisible to every
read endpoint (admin and non‑admin), but the row is retained so the removal
audit can still reference it. Permanent deletion is deferred to a later phase;
the `review_removals` surrogate id is retained in preparation.

## Scope
- Soft‑delete only — no permanent‑delete path this iteration.
- Non‑admin behaviour unchanged. Non‑admins cannot remove anything; the
  `deleted_at IS NULL` read filter applies uniformly to all roles.

## Decisions (agreed)
1. `reviews.deleted_at TIMESTAMPTZ` (nullable) — the soft‑delete marker. Every
   read and statistics query filters `deleted_at IS NULL`.
2. `review_removals` keeps a surrogate `id` PK (fresh UUID) and adds
   `review_id UUID NOT NULL UNIQUE REFERENCES reviews(id)`. The FK is retained
   now; its `ON DELETE` behaviour is deferred until permanent deletion lands.
3. `review_removals.created_by ... REFERENCES users(id) ON DELETE RESTRICT` —
   removes the contradictory `NOT NULL` + `SET NULL` pair (a deleting author is
   rejected by the FK instead of failing on `NOT NULL`).
4. Removal locks the live row (`FOR UPDATE`) before soft‑deleting → guarantees
   one removal per review and makes a re‑removal a clean 404.

## The diff to rectify (review feature vs `main`)
31 files / 4505 insertions. Files to change:
- `user-service/migrations/00006_review.sql` — **not a new migration** (feature
  has not shipped yet).
- `user-service/database/query/reviews.sql` + generated
  `user-service/internal/database/reviewdb/*` (regenerate sqlc).
- `user-service/internal/review/model.go`, `service.go`,
  `postgres_repository.go`, `repository.go`, `handler.go`.
- Tests: `service_test.go`, `postgres_repository_integration_test.go`.
- `plans/spec(user-service): add review feature.md` — soft‑delete semantics.

The integration test currently asserts (soft‑delete keeps most of it valid):
- After removal: `userStats` = 1 / 2 (the soft‑deleted review is excluded from
  the count/mean because `RevieweeStatistics` filters `deleted_at IS NULL`).
- `ListReviewRemovals` returns the 1 audit row with its totals.
- `ListReviewsForUser` no longer returns the removed review.
- A re‑`RemoveReview` of the removed review returns `errReviewNotFound`.

## Concrete changes

### 1. Migration `00006_review.sql`
- `reviews`: add `deleted_at TIMESTAMPTZ`.
- `review_removals`: keep surrogate `id`; add
  `review_id UUID NOT NULL UNIQUE REFERENCES reviews(id)`; change
  `created_by` to `ON DELETE RESTRICT`.

### 2. `database/query/reviews.sql` + regenerate sqlc
- Reads add `AND deleted_at IS NULL`: `FindReview` becomes
  `FindReviewLocked` (add `FOR UPDATE`), `ListReviewsForUser`,
  `ListReviewsFromUser`, `CountReviewsForUser`, `CountReviewsFromUser`,
  `RevieweeStatistics`.
- Add `SoftDeleteReview`:
  `UPDATE reviews SET deleted_at = now() WHERE id = @id::UUID AND deleted_at IS NULL;`
- `DeleteReview` (physical) is no longer used → dropped by sqlc
  `omit_unused_structs`.
- `InsertReviewRemoval` gains `@review_id::UUID`.

### 3. `repository.go` / `postgres_repository.go`
- `FindReview(ctx, q, id)` → `FindReviewLocked(ctx, q, id)` (row lock). The
  snapshot (reviewer/reviewee/order) comes from the same locked row — no
  relationship subquery, so `LANDMINES/locked-row-relationship-snapshots.md`
  does **not** apply.
- Remove `DeleteReview` from interface + impl + fakes.
- Add `SoftDeleteReview(ctx, q, id)`.
- `InsertReviewRemoval` gains `review_id`.
- `toReview` maps `deleted_at`. `mapReviewError` unchanged.

### 4. `model.go`
- `ReviewRemoval` gains `ReviewID uuid.UUID`.
- Doc comments: "hard‑deleted" → "removed".

### 5. `service.go` — `RemoveReview` (soft delete, one transaction)
1. `review, err := s.repo.FindReviewLocked(ctx, q, id)` → 404 on not found /
   already‑soft‑deleted.
2. `s.repo.SoftDeleteReview(ctx, q, id)`.
3. `removal.ReviewID = review.ID`; snapshot reviewer/reviewee/order;
   `InsertReviewRemoval`.
4. `s.repo.RecalculateStats(ctx, q, review.RevieweeID)`.
- Doc comment: "hard‑deletes a review" → "soft‑deletes a review (retains the
  row for the audit; stats recompute over non‑removed reviews)".

### 6. `handler.go`
- `RemoveReview` doc comment: "hard‑deletes a review on behalf of an admin" →
  "removes (soft‑deletes) a review on behalf of an admin".
- No other handler changes; the RPC stays admin‑only (interceptor unchanged).

### 7. Tests
- `postgres_repository_integration_test.go` / `service_test.go`: `FindReview`
  → `FindReviewLocked`, `DeleteReview` → `SoftDeleteReview` on the fakes; add
  an assertion that the soft‑deleted row has `deleted_at` set (and that
  `FindReviewLocked` returns not found on re‑remove); update the
  "The deleted review is gone" comment to reflect the retained row.
- `handler_test.go`: no signature change.

## Verification
- `make sqlc` after editing `reviews.sql` (regenerate `reviewdb/*`).
- `make fmt-check`, `make lint` from the repo root.
- `GOWORK=off go test ./...` in `user-service` (fast); integration test runs
  under `TEST_DATABASE_URL`.

## Deferred / out of scope
- Permanent delete: `DeleteReview` is removed now; the retained
  `review_removals.review_id` FK is guarded with `RESTRICT`/`NO ACTION` when
  permanent deletion is added (so a surviving audit row is not lost).
- `ListReviewRemovals` pagination already landed (`87e0bf9`) and is unaffected.
- Frontend: generated TypeScript only; no review UI changes.
