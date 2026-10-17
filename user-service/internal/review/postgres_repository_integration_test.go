package review

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// buildReviewService wires the review repository against a real database. Reads
// buildReviewService wires the review repository against a real database. Reads
// run against the shared pool; the transactional writes run on a database/sql
// transaction with the sqlc queries bound to it, so every insert, audit row and
// recalculation commits or rolls back together.
//
// stubOrders answers eligibility from a per-order table the test fills in;
// an unregistered order is not found.
type stubOrders struct {
	byOrder map[uuid.UUID]OrderParticipants
}

func (s *stubOrders) GetOrderParticipants(_ context.Context, orderID uuid.UUID) (OrderParticipants, error) {
	p, ok := s.byOrder[orderID]
	if !ok {
		return OrderParticipants{}, errOrderNotFound
	}
	return p, nil
}

func buildReviewService(t *testing.T, sqlDB *sql.DB, orders OrderGateway) *Service {
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
	return NewService(repo, withTx, orders)
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
	orders := &stubOrders{byOrder: map[uuid.UUID]OrderParticipants{}}
	service := buildReviewService(t, sqlDB, orders)

	t.Run("recalculates stats as reviews come and go", func(t *testing.T) {
		reviewer, second, reviewee, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		order1, order2 := uuid.New(), uuid.New()
		orders.byOrder[order1] = OrderParticipants{RequesterID: reviewer, CourierID: reviewee, Completed: true}
		orders.byOrder[order2] = OrderParticipants{RequesterID: second, CourierID: reviewee, Completed: true}
		for _, id := range []uuid.UUID{reviewer, second, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}

		if err := service.SubmitReview(ctx, reviewer, reviewee, order1, ReviewSubmission{Rating: 4, Comment: "good"}); err != nil {
			t.Fatalf("submit first review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 4 {
			t.Fatalf("after first review: count=%d avg=%v, want 1 and 4", c, avg)
		}

		if err := service.SubmitReview(ctx, second, reviewee, order2, ReviewSubmission{Rating: 2, Comment: "ok"}); err != nil {
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
		if len(removals.Removals) != 1 || removals.Removals[0].Reason != "abuse" || removals.Removals[0].CreatedBy != admin {
			t.Fatalf("audit row not recorded: %+v", removals)
		}
		if removals.TotalItems() != 1 || removals.TotalPages() != 1 {
			t.Fatalf("unexpected page info: %+v", removals.PageInfo)
		}
		// The removed review's row is retained with deleted_at set, so the
		// audit row can keep referencing it.
		var deletedAt sql.NullTime
		if err := sqlDB.QueryRowContext(ctx, "SELECT deleted_at FROM reviews WHERE id = $1", firstReview).Scan(&deletedAt); err != nil {
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

	t.Run("duplicate review is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		orders.byOrder[order] = OrderParticipants{RequesterID: reviewer, CourierID: reviewee, Completed: true}
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "first"}); err != nil {
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
		orders.byOrder[order] = OrderParticipants{RequesterID: reviewer, CourierID: reviewee, Completed: true}
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "first"}); err != nil {
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
		if err := service.SubmitReview(ctx, reviewee, reviewer, order, ReviewSubmission{Rating: 4, Comment: "other side"}); err != nil {
			t.Fatalf("other participant review: %v", err)
		}
	})

	t.Run("rating out of range is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		orders.byOrder[order] = OrderParticipants{RequesterID: reviewer, CourierID: reviewee, Completed: true}
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
