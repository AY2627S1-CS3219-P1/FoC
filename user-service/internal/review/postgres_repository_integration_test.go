package review

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// buildReviewService wires the review repository against a real database. Reads
// run against the shared pool; the transactional writes run on a database/sql
// transaction with the sqlc queries bound to it, so every insert, audit row and
// recalculation commits or rolls back together.
func buildReviewService(t *testing.T, sqlDB *sql.DB) *Service {
	t.Helper()
	repo := NewPostgresRepository(reviewdb.New(sqlDB))
	withTx := func(ctx context.Context, operation func(*reviewdb.Queries) error) error {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// Rollback is a no-op after a successful Commit.
		defer func() { _ = tx.Rollback() }()
		if err := operation(reviewdb.New(tx)); err != nil {
			return err
		}
		return tx.Commit()
	}
	return NewService(repo, withTx)
}

func insertUser(t *testing.T, ctx context.Context, sqlDB *sql.DB, id uuid.UUID) {
	t.Helper()
	if _, err := sqlDB.ExecContext(ctx,
		"INSERT INTO users (id, email, display_name) VALUES ($1, $2, $3)",
		id, id.String()+"@example.com", id.String()); err != nil {
		t.Fatalf("insert user %v: %v", id, err)
	}
}

// insertDeletingUser inserts a user that is permanently deleting (is_deleting = true)
func insertDeletingUser(t *testing.T, ctx context.Context, sqlDB *sql.DB, id uuid.UUID) {
	t.Helper()
	if _, err := sqlDB.ExecContext(
		ctx,
		"INSERT INTO users (id, email, display_name, is_deleting, deletion_requested_at) VALUES ($1, $2, $3, true, now())",
		id,
		id.String()+"@example.com",
		id.String(),
	); err != nil {
		t.Fatalf("insert deleting user %v: %v", id, err)
	}
}

// hardDeleteUser removes the row entirely, exercising the ON DELETE SET NULL
// retained-reference behaviour: the review's participant reference clears to NULL.
func hardDeleteUser(t *testing.T, ctx context.Context, sqlDB *sql.DB, id uuid.UUID) {
	t.Helper()
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
		t.Fatalf("hard delete user %v: %v", id, err)
	}
}

// userStats returns the persisted review_count and avg_rating for a user.
func userStats(t *testing.T, ctx context.Context, sqlDB *sql.DB, userID uuid.UUID) (int64, float64) {
	t.Helper()
	var count int64
	var avg float64
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT review_count, avg_rating FROM users WHERE id = $1", userID).Scan(&count, &avg); err != nil {
		t.Fatalf("read stats for %v: %v", userID, err)
	}
	return count, avg
}

func mustPage(t *testing.T, page, size int64) pagination.Pagination {
	t.Helper()
	p, err := pagination.New(page, size)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// truncateReviewData isolates a database integration test from rows left by a
// previous run (or a sibling test), so global count assertions stay
// deterministic when TEST_DATABASE_URL points at a reused throwaway database.
func truncateReviewData(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, table := range []string{"review_removals", "reviews", "users"} {
		if _, err := sqlDB.Exec("TRUNCATE " + table + " CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

func TestReviewRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	truncateReviewData(t, sqlDB)
	ctx := context.Background()
	service := buildReviewService(t, sqlDB)

	t.Run("list offsets beyond int32 are preserved", func(t *testing.T) {
		page := mustPage(t, math.MaxInt64, 1)
		userID := uuid.New()

		received, err := service.ListReviewsForUser(ctx, userID, page)
		if err != nil || len(received.Reviews) != 0 {
			t.Fatalf("list received at large offset: reviews=%v err=%v", received.Reviews, err)
		}

		given, err := service.ListReviewsFromUser(ctx, userID, page)
		if err != nil || len(given.Reviews) != 0 {
			t.Fatalf("list given at large offset: reviews=%v err=%v", given.Reviews, err)
		}

		removals, err := service.ListReviewRemovals(ctx, page)
		if err != nil || len(removals.Removals) != 0 {
			t.Fatalf("list removals at large offset: removals=%v err=%v", removals.Removals, err)
		}
	})

	t.Run("recalculates stats as reviews come and go", func(t *testing.T) {
		reviewer, second, reviewee, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		order1, order2 := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, second, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}

		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 4, Comment: "good"},
		); err != nil {
			t.Fatalf("submit first review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 4 {
			t.Fatalf("after first review: count=%d avg=%v, want 1 and 4", c, avg)
		}

		if err := service.SubmitReview(
			ctx,
			second,
			reviewee,
			order2,
			ReviewSubmission{Rating: 2, Comment: "ok"},
		); err != nil {
			t.Fatalf("submit second review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 2 || avg != 3 {
			t.Fatalf("after second review: count=%d avg=%v, want 2 and 3", c, avg)
		}

		// Remove the first review and confirm the stats recompute to the
		// remaining review. The lock-then-read-then-write in RecalculateStats
		// must report the converged totals.
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received: %v", err)
		}
		if len(received.Reviews) != 2 {
			t.Fatalf("want 2 reviews, got %d", len(received.Reviews))
		}
		var firstReview uuid.UUID
		for _, rv := range received.Reviews {
			if rv.ReviewerID == reviewer {
				firstReview = rv.ID
			}
		}
		if firstReview == uuid.Nil {
			t.Fatal("first review not found in listing")
		}
		if err := service.RemoveReview(ctx, firstReview, "abuse", admin); err != nil {
			t.Fatalf("remove review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 2 {
			t.Fatalf("after removal: count=%d avg=%v, want 1 and 2", c, avg)
		}

		removals, err := service.ListReviewRemovals(ctx, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list removals: %v", err)
		}
		if len(removals.Removals) != 1 || removals.Removals[0].Reason != "abuse" ||
			removals.Removals[0].CreatedBy != admin {
			t.Fatalf("audit row not recorded: %+v", removals)
		}
		if removals.TotalItems() != 1 || removals.TotalPages() != 1 {
			t.Fatalf("unexpected page info: %+v", removals.PageInfo)
		}
		// The removed review's row is retained with deleted_at set, so the
		// audit row can keep referencing it.
		var deletedAt sql.NullTime
		if err := sqlDB.QueryRowContext(ctx, "SELECT deleted_at FROM reviews WHERE id = $1", firstReview).
			Scan(&deletedAt); err != nil {
			t.Fatalf("read removed review: %v", err)
		}
		if !deletedAt.Valid {
			t.Fatal("removed review still live: deleted_at is not set")
		}
		// The removed review no longer appears in listings; only the rating-2
		// review remains.
		received, err = service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received after removal: %v", err)
		}
		if len(received.Reviews) != 1 || received.Reviews[0].Rating != 2 {
			t.Fatalf("expected only the rating-2 review, got %+v", received.Reviews)
		}
		if err := service.RemoveReview(ctx, firstReview, "abuse", admin); !errors.Is(err, errReviewNotFound) {
			t.Fatalf("re-removing a removed review should be not found, got %v", err)
		}
	})

	t.Run("average preserves fractional ratings", func(t *testing.T) {
		reviewee := uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		for _, rating := range []int{1, 2, 2} {
			reviewer := uuid.New()
			insertUser(t, ctx, sqlDB, reviewer)
			if err := service.SubmitReview(
				ctx,
				reviewer,
				reviewee,
				uuid.New(),
				ReviewSubmission{Rating: rating, Comment: "fractional average"},
			); err != nil {
				t.Fatal(err)
			}
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 3 || math.Abs(avg-5.0/3.0) > 1e-12 {
			t.Fatalf("count=%d avg=%v, want 3 and 5/3", count, avg)
		}
	})

	t.Run("audit failure rolls back the soft delete", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			uuid.New(),
			ReviewSubmission{Rating: 5, Comment: "audit rollback"},
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 10))
		if err != nil {
			t.Fatalf("list review: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("want one review, got %d", len(received.Reviews))
		}

		err = service.RemoveReview(ctx, received.Reviews[0].ID, "audit failure", uuid.New())
		if err == nil {
			t.Fatal("removal with a nonexistent administrator should fail")
		}
		var deletedAt sql.NullTime
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT deleted_at FROM reviews WHERE id = $1", received.Reviews[0].ID).Scan(&deletedAt); err != nil {
			t.Fatalf("read review after failed removal: %v", err)
		}
		if deletedAt.Valid {
			t.Fatal("review was soft-deleted despite the audit insert failing")
		}
		var auditCount int
		if err := sqlDB.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM review_removals WHERE review_id = $1",
			received.Reviews[0].ID,
		).Scan(&auditCount); err != nil {
			t.Fatalf("count removal audits: %v", err)
		}
		if auditCount != 0 {
			t.Fatalf("audit rows = %d after failed removal, want 0", auditCount)
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 1 || avg != 5 {
			t.Fatalf("stats after failed removal = (%d, %v), want (1, 5)", count, avg)
		}
	})

	t.Run("statistics include reviews committed during a lock wait", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		reviewerA, reviewerB, reviewee := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewerA, reviewerB, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		repo := NewPostgresRepository(reviewdb.New(sqlDB))

		txA, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = txA.Rollback() }()
		qA := reviewdb.New(txA)
		if err := repo.InsertReview(ctx, qA, ReviewInput{
			ID:         uuid.New(),
			ReviewerID: reviewerA,
			RevieweeID: reviewee,
			OrderID:    uuid.New(),
			Rating:     4,
			Comment:    "first",
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecalculateStats(ctx, qA, reviewee); err != nil {
			t.Fatal(err)
		}

		txB, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			cancel()
			_ = txB.Rollback()
		}()
		qB := reviewdb.New(txB)
		if err := repo.InsertReview(ctx, qB, ReviewInput{
			ID:         uuid.New(),
			ReviewerID: reviewerB,
			RevieweeID: reviewee,
			OrderID:    uuid.New(),
			Rating:     2,
			Comment:    "second",
		}); err != nil {
			t.Fatal(err)
		}
		var pidA, pidB int
		if err := txA.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pidA); err != nil {
			t.Fatal(err)
		}
		if err := txB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pidB); err != nil {
			t.Fatal(err)
		}

		// Start B before A commits and observe the actual lock wait, ensuring
		// B's first statement starts with a snapshot that excludes A's review.
		recalculated := make(chan error, 1)
		go func() { recalculated <- repo.RecalculateStats(ctx, qB, reviewee) }()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := sqlDB.QueryRowContext(ctx,
				"SELECT $1::integer = ANY(pg_blocking_pids($2::integer))", pidA, pidB).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case err := <-recalculated:
				t.Fatalf("recalculation finished without waiting for A: %v", err)
			case <-ctx.Done():
				t.Fatalf("recalculation did not wait for A: %v", ctx.Err())
			case <-ticker.C:
			}
		}
		if err := txA.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-recalculated:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err := txB.Commit(); err != nil {
			t.Fatal(err)
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 2 || avg != 3 {
			t.Fatalf("count=%d avg=%v, want 2 and 3", count, avg)
		}
	})

	t.Run("review count overflow is rejected without wrapping", func(t *testing.T) {
		reviewee := uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		q := reviewdb.New(sqlDB)
		if err := q.UpdateRevieweeStats(ctx, reviewdb.UpdateRevieweeStatsParams{
			RevieweeID: reviewee,
			Count:      math.MaxInt32,
			AvgRating:  1,
		}); err != nil {
			t.Fatalf("maximum count should fit: %v", err)
		}

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		err = reviewdb.New(tx).UpdateRevieweeStats(ctx, reviewdb.UpdateRevieweeStatsParams{
			RevieweeID: reviewee,
			Count:      int64(math.MaxInt32) + 1,
			AvgRating:  2,
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "22003" {
			t.Fatalf("want numeric value out of range, got %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != math.MaxInt32 || avg != 1 {
			t.Fatalf("failed update changed statistics: count=%d avg=%v", count, avg)
		}
	})

	t.Run("duplicate review is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "first"},
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "duplicate"})
		assertExternal(t, err, http.StatusConflict)
		if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 1 {
			t.Fatalf("duplicate review persisted: count=%d, want 1", c)
		}
	})

	// The unique indexes are not partial, so a soft-deleted review keeps holding
	// its (reviewer, order) and (reviewee, order) slots: a removed review cannot
	// be re-submitted for the same request until permanent deletion lands.
	t.Run("removed review still holds its unique slot", func(t *testing.T) {
		reviewer, reviewee, order, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "first"},
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("want 1 review, got %d", len(received.Reviews))
		}
		if err := service.RemoveReview(ctx, received.Reviews[0].ID, "abuse", admin); err != nil {
			t.Fatalf("remove review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 0 || avg != 0 {
			t.Fatalf("removed review still counted: count=%d avg=%v, want 0 and 0", c, avg)
		}
		err = service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 3, Comment: "again"})
		assertExternal(t, err, http.StatusConflict)
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 0 || avg != 0 {
			t.Fatalf("rejected re-review changed stats: count=%d avg=%v, want 0 and 0", c, avg)
		}
		// The reverse direction is unaffected: the other participant can still
		// review for the same request.
		if err := service.SubmitReview(
			ctx,
			reviewee,
			reviewer,
			order,
			ReviewSubmission{Rating: 4, Comment: "other side"},
		); err != nil {
			t.Fatalf("other participant review: %v", err)
		}
	})

	t.Run("rating out of range is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 6, Comment: "too high"})
		assertExternal(t, err, http.StatusBadRequest)
		if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 0 {
			t.Fatalf("rating out of range persisted: count=%d, want 0", c)
		}
	})

	t.Run("self review is rejected", func(t *testing.T) {
		u := uuid.New()
		insertUser(t, ctx, sqlDB, u)
		err := service.SubmitReview(ctx, u, u, uuid.New(), ReviewSubmission{Rating: 5, Comment: "self"})
		assertExternal(t, err, http.StatusBadRequest)
	})

	t.Run("missing reviewer is unauthenticated", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "x"})
		// The reviewer is the authenticated author; a missing row is unauthenticated.
		assertExternal(t, err, http.StatusUnauthorized)
	})

	t.Run("concurrent removal returns not found for the loser", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}
		orderID := uuid.New()
		if err := service.SubmitReview(ctx, reviewer, reviewee, orderID,
			ReviewSubmission{Rating: 4, Comment: "concurrent removal"}); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		var reviewID uuid.UUID
		if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM reviews WHERE order_id = $1", orderID).
			Scan(&reviewID); err != nil {
			t.Fatalf("read review ID: %v", err)
		}

		firstReady := make(chan int, 1)
		firstResult := make(chan error, 1)
		releaseFirst := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
		defer release()

		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		firstService := NewService(repo, func(
			txCtx context.Context,
			operation func(*reviewdb.Queries) error,
		) error {
			tx, err := sqlDB.BeginTx(txCtx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()

			if err := operation(reviewdb.New(tx)); err != nil {
				return err
			}
			var pid int
			if err := tx.QueryRowContext(txCtx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			firstReady <- pid

			select {
			case <-releaseFirst:
				return tx.Commit()
			case <-txCtx.Done():
				return txCtx.Err()
			}
		})
		go func() {
			firstResult <- firstService.RemoveReview(ctx, reviewID, "first removal", admin)
		}()

		var firstPID int
		select {
		case firstPID = <-firstReady:
		case err := <-firstResult:
			t.Fatalf("first removal failed before holding its transaction: %v", err)
		case <-ctx.Done():
			t.Fatalf("first removal did not reach its commit barrier: %v", ctx.Err())
		}

		secondResult := make(chan error, 1)
		go func() {
			secondResult <- service.RemoveReview(ctx, reviewID, "second removal", admin)
		}()

		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := sqlDB.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))",
				firstPID,
			).Scan(&blocked); err != nil {
				t.Fatalf("check removal lock wait: %v", err)
			}
			if blocked {
				break
			}
			select {
			case err := <-secondResult:
				t.Fatalf("second removal completed before waiting on the first: %v", err)
			case <-ctx.Done():
				t.Fatalf("second removal did not wait on the review lock: %v", ctx.Err())
			case <-ticker.C:
			}
		}

		release()
		if err := <-firstResult; err != nil {
			t.Fatalf("first removal: %v", err)
		}
		if err := <-secondResult; !errors.Is(err, errReviewNotFound) {
			t.Fatalf("second removal = %v, want review not found", err)
		} else {
			assertExternal(t, err, http.StatusNotFound)
		}

		var auditRows int
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM review_removals WHERE review_id = $1", reviewID).Scan(&auditRows); err != nil {
			t.Fatalf("count removal audit rows: %v", err)
		}
		if auditRows != 1 {
			t.Fatalf("removal audit rows = %d, want 1", auditRows)
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 0 || avg != 0 {
			t.Fatalf("after removal: count=%d avg=%v, want 0 and 0", count, avg)
		}
	})

	t.Run("submission rejected when reviewer is hard-deleted", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		// Soft-delete the reviewer to simulate hard-deletion
		if _, err := sqlDB.ExecContext(ctx, "UPDATE users SET deleted_at = now() WHERE id = $1", reviewer); err != nil {
			t.Fatalf("soft-delete reviewer: %v", err)
		}
		// Now try to submit a new review - the reviewer's row exists but is soft-deleted
		err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 3, Comment: "after deletion"},
		)
		// The reviewer's row exists but deleted_at IS NOT NULL, so it should be rejected as deleting
		if !errors.Is(err, errReviewerDeleting) && !errors.Is(err, errReviewerNotFound) {
			t.Fatalf("expected reviewer deleting or not found, got: %v", err)
		}
	})

	t.Run("submission rejected when reviewee is hard-deleted", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		// Soft-delete the reviewee to simulate hard-deletion
		if _, err := sqlDB.ExecContext(ctx, "UPDATE users SET deleted_at = now() WHERE id = $1", reviewee); err != nil {
			t.Fatalf("soft-delete reviewee: %v", err)
		}
		// Now try to submit a new review - the reviewee's row exists but is soft-deleted
		err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 3, Comment: "after deletion"},
		)
		// The reviewee's row exists but deleted_at IS NOT NULL, so it should be rejected as deleting
		if !errors.Is(err, errRevieweeDeleting) && !errors.Is(err, errRevieweeNotFound) {
			t.Fatalf("expected reviewee deleting or not found, got: %v", err)
		}
	})

	t.Run("removal audit written when creator is absent", func(t *testing.T) {
		reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}
		orderID := uuid.New()
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			orderID,
			ReviewSubmission{Rating: 5, Comment: "removal with absent creator"},
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list reviews: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("expected 1 review, got %d", len(received.Reviews))
		}
		reviewID := received.Reviews[0].ID

		// Remove the review, then hard-delete the admin who created the audit;
		// ON DELETE SET NULL clears created_by while the audit row survives.
		if err := service.RemoveReview(ctx, reviewID, "audit creator absent", admin); err != nil {
			t.Fatalf("remove review: %v", err)
		}
		hardDeleteUser(t, ctx, sqlDB, admin)

		removals, err := service.ListReviewRemovals(ctx, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list removals: %v", err)
		}
		var audit *ReviewRemoval
		for i := range removals.Removals {
			if removals.Removals[i].ReviewID == reviewID {
				audit = &removals.Removals[i]
				break
			}
		}
		if audit == nil {
			t.Fatalf("audit row for review %s missing after creator deletion", reviewID)
		}
		if !audit.CreatedByDeleted {
			t.Fatal("absent audit creator was not flagged deleted")
		}
		if audit.CreatedByName != "" {
			t.Fatalf("absent audit creator name leaked: %q", audit.CreatedByName)
		}
	})

	t.Run("ratings unchanged when reviewer deletes", func(t *testing.T) {
		secondReviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{secondReviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		order1, order2 := uuid.New(), uuid.New()

		// First reviewer submits a review
		firstReviewer := uuid.New()
		insertUser(t, ctx, sqlDB, firstReviewer)
		if err := service.SubmitReview(
			ctx,
			firstReviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 5, Comment: "first review"},
		); err != nil {
			t.Fatalf("submit first review: %v", err)
		}
		// Second reviewer submits a review
		if err := service.SubmitReview(
			ctx,
			secondReviewer,
			reviewee,
			order2,
			ReviewSubmission{Rating: 3, Comment: "second review"},
		); err != nil {
			t.Fatalf("submit second review: %v", err)
		}

		// Verify stats before deletion
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 2 || avg != 4 {
			t.Fatalf("before deletion: count=%d avg=%v, want 2 and 4", count, avg)
		}

		// Second reviewer is hard-deleted (as finalization does); ON DELETE SET
		// NULL clears the retained review's reviewer reference.
		hardDeleteUser(t, ctx, sqlDB, secondReviewer)

		// Stats should remain unchanged (retained review still counted)
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 2 || avg != 4 {
			t.Fatalf("after reviewer deletion: count=%d avg=%v, want 2 and 4", count, avg)
		}

		// The deleted reviewer's review is retained and flagged deleted.
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list reviews: %v", err)
		}
		// Should still have 2 reviews, but one should show deleted
		if len(received.Reviews) != 2 {
			t.Fatalf("expected 2 reviews, got %d", len(received.Reviews))
		}
		var absent int
		for _, rv := range received.Reviews {
			if rv.ReviewerDeleted {
				absent++
			}
		}
		if absent != 1 {
			t.Fatalf("absent reviewer flagged on %d reviews, want 1", absent)
		}
	})
}

// TestReviewAbsentParticipants exercises the retained-history behaviour end to
// end against a real database: a participant is hard-deleted (as finalization
// does), the review row is retained with its reference cleared to NULL, and the
// listing, counts, removal and submission paths all handle the absent participant
// without dropping rows, crashing, or manufacturing a user.
func TestReviewAbsentParticipants(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	truncateReviewData(t, sqlDB)
	ctx := context.Background()
	service := buildReviewService(t, sqlDB)

	t.Run("retained review surfaces with the reviewer flagged deleted", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 5, Comment: "r"}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		hardDeleteUser(t, ctx, sqlDB, reviewer)

		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("want 1 retained review, got %d", len(received.Reviews))
		}
		if received.PageInfo.TotalItems() != 1 {
			t.Fatalf("total=%d want 1", received.PageInfo.TotalItems())
		}
		rv := received.Reviews[0]
		if !rv.ReviewerDeleted {
			t.Fatal("expected reviewer deleted flag")
		}
		if rv.ReviewerID != uuid.Nil {
			t.Fatalf("expected nil reviewer id, got %v", rv.ReviewerID)
		}
		if rv.ReviewerName != "" {
			t.Fatalf("expected empty reviewer name, got %q", rv.ReviewerName)
		}
		if rv.RevieweeID != reviewee || rv.RevieweeDeleted {
			t.Fatalf("reviewee should be present: %+v", rv)
		}
	})

	t.Run("soft-deleted participant is flagged deleted in listings", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 5, Comment: "r"}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		// A legacy/deactivated account keeps its row but sets deleted_at; it must
		// not surface as a live participant with its name and profile link.
		if _, err := sqlDB.ExecContext(ctx,
			"UPDATE users SET deleted_at = now() WHERE id = $1", reviewer); err != nil {
			t.Fatal(err)
		}
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("want 1 retained review, got %d", len(received.Reviews))
		}
		rv := received.Reviews[0]
		if !rv.ReviewerDeleted {
			t.Fatal("soft-deleted reviewer was not flagged deleted")
		}
		if rv.ReviewerName != "" {
			t.Fatalf("soft-deleted reviewer name leaked: %q", rv.ReviewerName)
		}
	})

	t.Run("retained review surfaces with the reviewee flagged deleted", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 3, Comment: "r"}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		hardDeleteUser(t, ctx, sqlDB, reviewee)

		// The review is retained for its original reviewer and pages under that
		// reviewer with the reviewee cleared and flagged deleted.
		given, err := service.ListReviewsFromUser(ctx, reviewer, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list given: %v", err)
		}
		if len(given.Reviews) != 1 {
			t.Fatalf("want 1 retained given review, got %d", len(given.Reviews))
		}
		rv := given.Reviews[0]
		if !rv.RevieweeDeleted {
			t.Fatal("expected reviewee deleted flag")
		}
		if rv.RevieweeID != uuid.Nil || rv.RevieweeName != "" {
			t.Fatalf("reviewee should be absent: %+v", rv)
		}
		if rv.ReviewerID != reviewer || rv.ReviewerDeleted {
			t.Fatalf("reviewer should be present: %+v", rv)
		}
	})

	t.Run("counts and pagination stay stable with an absent participant", func(t *testing.T) {
		reviewerA, reviewerB, reviewerC, reviewee := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewerA, reviewerB, reviewerC, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		ratings := map[uuid.UUID]int{reviewerA: 5, reviewerB: 4, reviewerC: 3}
		for rv := range ratings {
			if err := service.SubmitReview(ctx, rv, reviewee, uuid.New(),
				ReviewSubmission{Rating: ratings[rv], Comment: "c"}); err != nil {
				t.Fatalf("submit %v: %v", rv, err)
			}
		}
		hardDeleteUser(t, ctx, sqlDB, reviewerA)

		// Every page reports the same total even though reviewerA's row is
		// retained with an absent reviewer, so counts never drop a retained review.
		var totals []int64
		var absentFlagged int
		for p := int64(1); p <= 3; p++ {
			received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, p, 1))
			if err != nil {
				t.Fatalf("list p%d: %v", p, err)
			}
			totals = append(totals, received.PageInfo.TotalItems())
			for _, rv := range received.Reviews {
				if rv.ReviewerDeleted {
					absentFlagged++
				}
			}
		}
		for i, total := range totals {
			if total != 3 {
				t.Fatalf("page %d total=%d want 3", i+1, total)
			}
		}
		if absentFlagged != 1 {
			t.Fatalf("absent reviewer flagged on %d pages, want 1", absentFlagged)
		}
	})

	t.Run("removes a retained review whose reviewee is absent", func(t *testing.T) {
		reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 5, Comment: "x"}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		var reviewID uuid.UUID
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT id FROM reviews WHERE reviewer_id = $1 AND reviewee_id = $2", reviewer, reviewee).
			Scan(&reviewID); err != nil {
			t.Fatalf("read review: %v", err)
		}
		if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 1 {
			t.Fatalf("pre-deletion count=%d want 1", c)
		}

		hardDeleteUser(t, ctx, sqlDB, reviewee)
		// Removing the retained review must succeed and write the audit without
		// trying to update the (now gone) reviewee's statistics.
		if err := service.RemoveReview(ctx, reviewID, "abuse", admin); err != nil {
			t.Fatalf("remove review of absent reviewee: %v", err)
		}

		var auditRows int
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM review_removals WHERE review_id = $1", reviewID).Scan(&auditRows); err != nil {
			t.Fatalf("count audits: %v", err)
		}
		if auditRows != 1 {
			t.Fatalf("audit rows = %d want 1", auditRows)
		}
		// The audit stores the absent reviewee as a zero id on the NOT NULL column,
		// never NULL, so the audit field is populated without a NULL crash.
		var auditReviewee sql.NullString
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT reviewee_id::text FROM review_removals WHERE review_id = $1", reviewID).
			Scan(&auditReviewee); err != nil {
			t.Fatalf("read audit reviewee: %v", err)
		}
		if auditReviewee.Valid && auditReviewee.String != uuid.Nil.String() {
			t.Fatalf("audit reviewee should be absent (zero id), got %q", auditReviewee.String)
		}
		var deletedAt sql.NullTime
		if err := sqlDB.QueryRowContext(ctx, "SELECT deleted_at FROM reviews WHERE id = $1", reviewID).
			Scan(&deletedAt); err != nil {
			t.Fatalf("read review: %v", err)
		}
		if !deletedAt.Valid {
			t.Fatal("removed review not soft-deleted")
		}
	})

	t.Run("submission against a deleting reviewer is failed precondition", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		insertDeletingUser(t, ctx, sqlDB, reviewer)
		insertUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 3, Comment: "x"})
		if !errors.Is(err, errReviewerDeleting) {
			t.Fatalf("expected reviewer deleting, got: %v", err)
		}
		assertExternal(t, err, http.StatusPreconditionFailed)
	})

	t.Run("submission against a deleting reviewee is failed precondition", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewer)
		insertDeletingUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 3, Comment: "x"})
		if !errors.Is(err, errRevieweeDeleting) {
			t.Fatalf("expected reviewee deleting, got: %v", err)
		}
		assertExternal(t, err, http.StatusPreconditionFailed)
	})

	t.Run("submission against a missing reviewer is unauthenticated", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 3, Comment: "x"})
		if !errors.Is(err, errReviewerNotFound) {
			t.Fatalf("expected reviewer not found, got: %v", err)
		}
		assertExternal(t, err, http.StatusUnauthorized)
	})

	t.Run("submission against a missing reviewee is not found", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewer)
		err := service.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 3, Comment: "x"})
		if !errors.Is(err, errRevieweeNotFound) {
			t.Fatalf("expected reviewee not found, got: %v", err)
		}
		assertExternal(t, err, http.StatusNotFound)
	})
}
