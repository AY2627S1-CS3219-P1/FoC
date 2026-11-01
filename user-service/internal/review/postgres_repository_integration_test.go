package review

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"os"
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

func mustPage(t *testing.T, page, size int) pagination.Pagination {
	t.Helper()
	p, err := pagination.New(page, size)
	if err != nil {
		t.Fatal(err)
	}
	return p
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
	ctx := context.Background()
	service := buildReviewService(t, sqlDB)

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

	t.Run("unknown reviewer is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "x"})
		assertExternal(t, err, http.StatusBadRequest)
	})
}
