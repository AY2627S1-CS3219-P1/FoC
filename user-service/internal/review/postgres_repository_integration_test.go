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
	return NewService(repo, withTx, nil)
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

func mustPage(t *testing.T, page, size int64) pagination.Pagination {
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
			nil,
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
			nil,
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
				nil,
			); err != nil {
				t.Fatal(err)
			}
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 3 || math.Abs(avg-5.0/3.0) > 1e-12 {
			t.Fatalf("count=%d avg=%v, want 3 and 5/3", count, avg)
		}
	})

	// The guarded attachment and cleanup-claim statements are the photo
	// consistency boundary; fakes cannot exercise their SQL predicates, so
	// they run against the real database here.
	t.Run("guarded attachment claims a staged photo exactly once", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "with photo"},
			nil,
		); err != nil {
			t.Fatalf("submit review for attachment: %v", err)
		}
		reviewed, err := service.ListReviewsFromUser(ctx, reviewer, mustPage(t, 1, 1))
		if err != nil || len(reviewed.Reviews) != 1 {
			t.Fatalf("list submitted review: reviews=%v err=%v", reviewed.Reviews, err)
		}
		reviewID := reviewed.Reviews[0].ID

		insertPhoto := func(t *testing.T) ReviewPhoto {
			t.Helper()
			input := ReviewPhotoInput{
				ID:              uuid.New(),
				OwnerID:         reviewer,
				ObjectKey:       "review/photo/" + uuid.New().String(),
				ContentType:     "image/png",
				SizeBytes:       1234,
				UploadExpiresAt: time.Now().Add(time.Hour),
			}
			if err := repo.InsertReviewPhoto(ctx, input); err != nil {
				t.Fatalf("stage photo: %v", err)
			}
			staged, err := repo.GetReviewPhoto(ctx, reviewer, input.ID)
			if err != nil {
				t.Fatalf("load staged photo: %v", err)
			}
			return staged
		}
		q := reviewdb.New(sqlDB)

		staged := insertPhoto(t)
		// Attachment is transactional in production; the plain pool handle
		// exercises the same guarded UPDATE.
		rows, err := repo.AttachReviewPhoto(ctx, q, reviewer, reviewID, 0, staged)
		if err != nil || rows != 1 {
			t.Fatalf("first attach: rows=%d err=%v, want 1 and nil", rows, err)
		}
		attached, err := repo.ListReviewPhotosForReviews(ctx, []uuid.UUID{reviewID})
		if err != nil || len(attached) != 1 {
			t.Fatalf("list attached: photos=%v err=%v", attached, err)
		}
		if attached[0].Ordinal != 0 || attached[0].ObjectKey != staged.ObjectKey {
			t.Fatalf("attached photo mismatch: %+v", attached[0])
		}

		// The state guard blocks a second attachment of the same photo.
		rows, err = repo.AttachReviewPhoto(ctx, q, reviewer, reviewID, 1, staged)
		if err != nil || rows != 0 {
			t.Fatalf("second attach: rows=%d err=%v, want 0 and nil", rows, err)
		}

		// A shape mismatch — the stored object replaced between inspection and
		// attach — also matches zero rows and must fail the submission.
		replaced := staged
		replaced.SizeBytes = staged.SizeBytes + 1
		rows, err = repo.AttachReviewPhoto(ctx, q, reviewer, reviewID, 1, replaced)
		if err != nil || rows != 0 {
			t.Fatalf("attach with drifted shape: rows=%d err=%v, want 0 and nil", rows, err)
		}

		// A photo staged by another user is never attachable.
		stranger := uuid.New()
		insertUser(t, ctx, sqlDB, stranger)
		foreign := insertPhoto(t)
		foreign.OwnerID = stranger
		rows, err = repo.AttachReviewPhoto(ctx, q, stranger, reviewID, 1, foreign)
		if err != nil || rows != 0 {
			t.Fatalf("attach of a foreign photo: rows=%d err=%v, want 0 and nil", rows, err)
		}
	})

	t.Run("cleanup claims expired orphan staging rows only", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		owner := uuid.New()
		insertUser(t, ctx, sqlDB, owner)

		stage := func(t *testing.T, expiresAt time.Time) ReviewPhotoInput {
			t.Helper()
			input := ReviewPhotoInput{
				ID:              uuid.New(),
				OwnerID:         owner,
				ObjectKey:       "review/photo/" + uuid.New().String(),
				ContentType:     "image/jpeg",
				SizeBytes:       42,
				UploadExpiresAt: expiresAt,
			}
			if err := repo.InsertReviewPhoto(ctx, input); err != nil {
				t.Fatalf("stage photo: %v", err)
			}
			return input
		}
		now := time.Now().UTC()
		expired := stage(t, now.Add(-time.Hour))
		live := stage(t, now.Add(time.Hour))

		// The claim horizon sits slightly ahead of the inserts, like the
		// production ticker running after the rows were created.
		horizon := now.Add(time.Minute)
		claimed, err := repo.ClaimOrphanReviewPhotos(ctx, horizon, horizon)
		if err != nil {
			t.Fatalf("claim orphans: %v", err)
		}
		claimedIDs := map[uuid.UUID]bool{}
		for _, row := range claimed {
			claimedIDs[row.ID] = true
		}
		if !claimedIDs[expired.ID] {
			t.Fatal("expired orphan staging row was not claimed")
		}
		if claimedIDs[live.ID] {
			t.Fatal("unexpired staging row must not be claimed")
		}

		rows, err := repo.DeleteOrphanReviewPhoto(ctx, expired.ID)
		if err != nil || rows != 1 {
			t.Fatalf("delete claimed orphan: rows=%d err=%v, want 1 and nil", rows, err)
		}
		// A claimed row is only deletable in the deleting state; the live row
		// stays untouched.
		if rows, err := repo.DeleteOrphanReviewPhoto(ctx, live.ID); err != nil || rows != 0 {
			t.Fatalf("delete unclaimed row: rows=%d err=%v, want 0 and nil", rows, err)
		}
	})

	t.Run("batched reviewed-order lookup and single fetch", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		order1, order2 := uuid.New(), uuid.New()
		if err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 5, Comment: "reviewed"},
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}

		got, err := repo.ListReviewedOrderIds(ctx, reviewer, []uuid.UUID{order1, order2})
		if err != nil {
			t.Fatalf("list reviewed order ids: %v", err)
		}
		if len(got) != 1 || got[0] != order1 {
			t.Fatalf("reviewed ids = %v, want only %v", got, order1)
		}

		review, err := repo.ReviewForOrder(ctx, reviewer, order1)
		if err != nil || review.Rating != 5 || review.OrderID != order1 {
			t.Fatalf("review for order = %+v err=%v", review, err)
		}
		if _, err := repo.ReviewForOrder(ctx, reviewer, order2); !errors.Is(err, errReviewNotFound) {
			t.Fatalf("unreviewed order should be not found, got %v", err)
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
			nil,
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
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "duplicate"},
			nil,
		)
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
			nil,
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
		err = service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 3, Comment: "again"}, nil)
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
			nil,
		); err != nil {
			t.Fatalf("other participant review: %v", err)
		}
	})

	t.Run("rating out of range is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 6, Comment: "too high"},
			nil,
		)
		assertExternal(t, err, http.StatusBadRequest)
		if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 0 {
			t.Fatalf("rating out of range persisted: count=%d, want 0", c)
		}
	})

	t.Run("self review is rejected", func(t *testing.T) {
		u := uuid.New()
		insertUser(t, ctx, sqlDB, u)
		err := service.SubmitReview(ctx, u, u, uuid.New(), ReviewSubmission{Rating: 5, Comment: "self"}, nil)
		assertExternal(t, err, http.StatusBadRequest)
	})

	t.Run("unknown reviewer is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "x"}, nil)
		assertExternal(t, err, http.StatusBadRequest)
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
			ReviewSubmission{Rating: 4, Comment: "concurrent removal"}, nil); err != nil {
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
		}, nil)
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

}
