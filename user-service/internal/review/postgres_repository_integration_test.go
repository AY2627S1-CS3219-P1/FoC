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

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	moderationeventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/orders"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Use the production completion path to publish fixtures and update ratings.
func buildReviewService(t *testing.T, sqlDB *sql.DB) (*Service, *CompletionHandler) {
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
	eligible := &eligibleOrders{Repository: repo}
	return NewService(eligible, withTx, nil, eligible), NewCompletionHandler(repo, withTx)
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

func reviewRowID(t *testing.T, ctx context.Context, sqlDB *sql.DB, orderID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM reviews WHERE order_id = $1", orderID).Scan(&id); err != nil {
		t.Fatalf("read review ID for order %v: %v", orderID, err)
	}
	return id
}

func approveReview(
	t *testing.T,
	ctx context.Context,
	completions *CompletionHandler,
	sqlDB *sql.DB,
	reviewID uuid.UUID,
) {
	t.Helper()
	var reviewerID, requestID uuid.NullUUID
	var revision int32
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT reviewer_id, moderation_request_id, content_revision FROM reviews WHERE id = $1", reviewID,
	).Scan(&reviewerID, &requestID, &revision); err != nil {
		t.Fatalf("read review %v for approval: %v", reviewID, err)
	}
	if !requestID.Valid {
		t.Fatalf("review %v has no moderation request id", reviewID)
	}
	event := approvedCompletionEvent(reviewID, reviewerID.UUID, requestID.UUID, revision, 1)
	if err := completions.Handle(ctx, moderationMetadata(), event); err != nil {
		t.Fatalf("apply approval to review %v: %v", reviewID, err)
	}
}

func moderationMetadata() kafka.Metadata {
	return kafka.Metadata{Topic: ModerationFinishedTopic}
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
	service, completions := buildReviewService(t, sqlDB)

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

		unpublished, err := service.ListUnpublishedReviewsForUser(ctx, userID, page)
		if err != nil || len(unpublished.Reviews) != 0 {
			t.Fatalf("list unpublished at large offset: reviews=%v err=%v", unpublished.Reviews, err)
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

		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 4, Comment: "good"},
			nil,
		); err != nil {
			t.Fatalf("submit first review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 0 || avg != 0 {
			t.Fatalf("pending review changed stats: count=%d avg=%v, want 0 and 0", c, avg)
		}
		firstUnpublished, err := service.ListUnpublishedReviewsForUser(
			ctx,
			reviewer,
			mustPage(t, 1, 20),
		)
		if err != nil || len(firstUnpublished.Reviews) != 1 {
			t.Fatalf("pending review not in the author's unpublished list: %+v err=%v", firstUnpublished.Reviews, err)
		}
		firstReview := firstUnpublished.Reviews[0].ID
		if firstUnpublished.Reviews[0].ModerationStatus != ModerationPending {
			t.Fatalf("status = %q, want pending", firstUnpublished.Reviews[0].ModerationStatus)
		}

		approveReview(t, ctx, completions, sqlDB, firstReview)
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 4 {
			t.Fatalf("after approval: count=%d avg=%v, want 1 and 4", c, avg)
		}

		if _, err := service.SubmitReview(
			ctx,
			second,
			reviewee,
			order2,
			ReviewSubmission{Rating: 2, Comment: "ok"},
			nil,
		); err != nil {
			t.Fatalf("submit second review: %v", err)
		}
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received: %v", err)
		}
		if len(received.Reviews) != 1 || received.Reviews[0].ReviewerID != reviewer {
			t.Fatalf("pending review visible to the reviewee: %+v", received.Reviews)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 4 {
			t.Fatalf("pending second review changed stats: count=%d avg=%v, want 1 and 4", c, avg)
		}

		secondUnpublished, err := service.ListUnpublishedReviewsForUser(
			ctx,
			second,
			mustPage(t, 1, 20),
		)
		if err != nil || len(secondUnpublished.Reviews) != 1 {
			t.Fatalf("pending second review not listed: %+v err=%v", secondUnpublished.Reviews, err)
		}
		secondReview := secondUnpublished.Reviews[0].ID
		approveReview(t, ctx, completions, sqlDB, secondReview)
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 2 || avg != 3 {
			t.Fatalf("after second approval: count=%d avg=%v, want 2 and 3", c, avg)
		}

		// Remove the first review and confirm the stats recompute to the
		// remaining review. The lock-then-read-then-write in RecalculateStats
		// must report the converged totals.
		received, err = service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received: %v", err)
		}
		if len(received.Reviews) != 2 {
			t.Fatalf("want 2 reviews, got %d", len(received.Reviews))
		}
		beforeRemovals, err := service.ListReviewRemovals(ctx, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list removals before deletion: %v", err)
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
		if len(removals.Removals) == 0 || removals.Removals[0].ReviewID != firstReview ||
			removals.Removals[0].Reason != "abuse" ||
			removals.Removals[0].CreatedBy != admin {
			t.Fatalf("audit row not recorded: %+v", removals)
		}
		if removals.TotalItems() != beforeRemovals.TotalItems()+1 {
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
			order := uuid.New()
			if _, err := service.SubmitReview(
				ctx,
				reviewer,
				reviewee,
				order,
				ReviewSubmission{Rating: rating, Comment: "fractional average"},
				nil,
			); err != nil {
				t.Fatal(err)
			}
			approveReview(t, ctx, completions, sqlDB, reviewRowID(t, ctx, sqlDB, order))
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 3 || math.Abs(avg-5.0/3.0) > 1e-12 {
			t.Fatalf("count=%d avg=%v, want 3 and 5/3", count, avg)
		}
	})

	// The guarded attachment and cleanup-claim statements are the photo
	// consistency boundary; fakes cannot exercise their SQL predicates, so
	// they run against the real database here.
	t.Run("submitted photos remain private until approval", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		photoID := uuid.New()
		key := "review/photo/" + photoID.String()
		if err := repo.InsertReviewPhoto(ctx, ReviewPhotoInput{
			ID: photoID, OwnerID: reviewer, ObjectKey: key, ContentType: "image/png", SizeBytes: 1234,
			UploadExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		photoService, photoCompletions := buildReviewService(t, sqlDB)
		photoService.photos = &fakeStorage{objects: map[string]images.ObjectInfo{
			key: {Exists: true, Size: 1234, ContentType: "image/png"},
		}}
		result, err := photoService.SubmitReview(ctx, reviewer, reviewee, uuid.New(),
			ReviewSubmission{Rating: 5, Comment: "Careful delivery"}, []uuid.UUID{photoID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != ModerationPending {
			t.Fatalf("submission status = %s", result.Status)
		}
		photos, err := repo.ListReviewPhotosForReviews(ctx, []uuid.UUID{result.ReviewID})
		if err != nil || len(photos) != 1 || photos[0].ID != photoID {
			t.Fatalf("attachment: %+v, %v", photos, err)
		}
		page, err := photoService.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil || len(page.Reviews) != 0 {
			t.Fatalf("pending review exposed: %+v, %v", page, err)
		}
		if count, _ := userStats(t, ctx, sqlDB, reviewee); count != 0 {
			t.Fatal("pending photo review changed ratings")
		}
		approveReview(t, ctx, photoCompletions, sqlDB, result.ReviewID)
		page, err = photoService.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil || len(page.Reviews) != 1 || len(page.Reviews[0].Photos) != 1 ||
			page.Reviews[0].Photos[0].URL == "" {
			t.Fatalf("approved photos missing: %+v, %v", page, err)
		}
		if count, avg := userStats(t, ctx, sqlDB, reviewee); count != 1 || avg != 5 {
			t.Fatalf("approved stats: %d, %v", count, avg)
		}
	})

	t.Run("guarded attachment claims a staged photo exactly once", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "with photo"},
			nil,
		); err != nil {
			t.Fatalf("submit review for attachment: %v", err)
		}
		approveReview(t, ctx, completions, sqlDB, reviewRowID(t, ctx, sqlDB, order))
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
		foreign.OwnerID = uuid.NullUUID{UUID: stranger, Valid: true}
		rows, err = repo.AttachReviewPhoto(ctx, q, stranger, reviewID, 1, foreign)
		if err != nil || rows != 0 {
			t.Fatalf("attach of a foreign photo: rows=%d err=%v, want 0 and nil", rows, err)
		}
	})

	t.Run("deleting the uploader keeps the photo with a null owner", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		owner := uuid.New()
		insertUser(t, ctx, sqlDB, owner)
		id := uuid.New()
		if err := repo.InsertReviewPhoto(ctx, ReviewPhotoInput{
			ID:              id,
			OwnerID:         owner,
			ObjectKey:       "review/photo/" + uuid.New().String(),
			ContentType:     "image/png",
			SizeBytes:       10,
			UploadExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("stage photo: %v", err)
		}
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", owner); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		var ownerID uuid.NullUUID
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT owner_id FROM review_photos WHERE id = $1", id).Scan(&ownerID); err != nil {
			t.Fatalf("photo must survive owner deletion: %v", err)
		}
		if ownerID.Valid {
			t.Fatalf("owner_id = %v, want NULL", ownerID.UUID)
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

	t.Run("cleanup preserves staging until the actual upload grant expires", func(t *testing.T) {
		owner := uuid.New()
		insertUser(t, ctx, sqlDB, owner)
		storage := &fakeStorage{uploadTTL: 48 * time.Hour}
		photoService, _ := buildReviewService(t, sqlDB)
		photoService.photos = storage
		photoID, grant, err := photoService.CreateReviewPhotoUpload(ctx, owner, "image/jpeg", 42)
		if err != nil {
			t.Fatal(err)
		}
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		photo, err := repo.GetReviewPhoto(ctx, owner, photoID)
		if err != nil || photo.UploadExpiresAt.Sub(grant.ExpiresAt).Abs() > time.Microsecond {
			t.Fatalf("persisted expiry = %v, grant = %v, err=%v", photo.UploadExpiresAt, grant.ExpiresAt, err)
		}
		for _, hours := range []int{25, 49} {
			now := grant.ExpiresAt.Add(time.Duration(hours-48) * time.Hour)
			claimed, err := repo.ClaimOrphanReviewPhotos(ctx, now.Add(-photoOrphanAfter), now)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range claimed {
				if row.ID == photoID {
					found = true
				}
			}
			if found != (hours == 49) {
				t.Fatalf("claimed at hour %d = %v", hours, found)
			}
		}
	})

	t.Run("batched reviewed-order lookup and single fetch", func(t *testing.T) {
		repo := NewPostgresRepository(reviewdb.New(sqlDB))
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		order1, order2 := uuid.New(), uuid.New()
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 5, Comment: "reviewed"},
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}

		got, err := repo.ListReviewOrderStatuses(ctx, reviewer, []uuid.UUID{order1, order2})
		if err != nil {
			t.Fatalf("list reviewed order ids: %v", err)
		}
		if len(got.ReviewedOrderIDs) != 1 || got.ReviewedOrderIDs[0] != order1 || len(got.RemovedOrderIDs) != 0 {
			t.Fatalf("reviewed ids = %v, want only %v", got, order1)
		}

		review, err := repo.ReviewForOrder(ctx, reviewer, order1)
		if err != nil || review.Rating != 5 || review.OrderID != order1 {
			t.Fatalf("review for order = %+v err=%v", review, err)
		}
		if _, err := repo.ReviewForOrder(ctx, reviewer, order2); !errors.Is(err, errReviewNotFound) {
			t.Fatalf("unreviewed order should be not found, got %v", err)
		}
		if err := service.RemoveReview(ctx, review.ID, "abuse", reviewer); err != nil {
			t.Fatalf("remove review: %v", err)
		}
		statuses, err := service.ListMyReviewsForOrders(ctx, reviewer, []uuid.UUID{order1, order2})
		if err != nil || len(statuses.ReviewedOrderIDs) != 0 || len(statuses.RemovedOrderIDs) != 1 ||
			statuses.RemovedOrderIDs[0] != order1 {
			t.Fatalf("removed order status = %+v err=%v", statuses, err)
		}
		removed, err := service.GetMyReviewForOrder(ctx, reviewer, order1)
		if !errors.Is(err, errReviewRemoved) || removed.ID != uuid.Nil || removed.Comment != "" ||
			len(removed.Photos) != 0 {
			t.Fatalf("removed review must expose no content: %+v err=%v", removed, err)
		}
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order1,
			ReviewSubmission{Rating: 5, Comment: "replacement"},
			nil,
		); !errors.Is(
			err,
			errReviewAlreadyExists,
		) {
			t.Fatalf("removed review must still block resubmission: %v", err)
		}
		other, err := service.ListMyReviewsForOrders(ctx, reviewee, []uuid.UUID{order1})
		if err != nil || len(other.ReviewedOrderIDs) != 0 || len(other.RemovedOrderIDs) != 0 {
			t.Fatalf("statuses must be caller-scoped: %+v err=%v", other, err)
		}
		if _, err := service.GetMyReviewForOrder(ctx, reviewee, order1); !errors.Is(err, errReviewNotFound) {
			t.Fatalf("another reviewer must not see the removed status: %v", err)
		}

	})

	t.Run("audit failure rolls back the soft delete", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		order := uuid.New()
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "audit rollback"},
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		approveReview(t, ctx, completions, sqlDB, reviewRowID(t, ctx, sqlDB, order))
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
		reviewA := uuid.New()
		if _, err := repo.InsertReview(ctx, qA, ReviewInput{
			ID:                  reviewA,
			ReviewerID:          reviewerA,
			RevieweeID:          reviewee,
			OrderID:             uuid.New(),
			Rating:              4,
			Comment:             "first",
			ModerationRequestID: uuid.New(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ApplyModerationOutcome(ctx, qA, reviewA, ModerationCompletion{
			Status:        ModerationApproved,
			RunGeneration: 1,
			ModeratedAt:   time.Now().UTC(),
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
		reviewB := uuid.New()
		if _, err := repo.InsertReview(ctx, qB, ReviewInput{
			ID:                  reviewB,
			ReviewerID:          reviewerB,
			RevieweeID:          reviewee,
			OrderID:             uuid.New(),
			Rating:              2,
			Comment:             "second",
			ModerationRequestID: uuid.New(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ApplyModerationOutcome(ctx, qB, reviewB, ModerationCompletion{
			Status:        ModerationApproved,
			RunGeneration: 1,
			ModeratedAt:   time.Now().UTC(),
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
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "first"},
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		_, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "duplicate"}, nil,
		)
		assertExternal(t, err, http.StatusConflict)
		if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 0 {
			t.Fatalf("duplicate review persisted: count=%d, want 0", c)
		}
		// The duplicate must not leave a second outbox row: the failed insert
		// rolled the whole transaction back.
		var outboxRows int
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM review_submitted_outbox o JOIN reviews r ON r.id = o.review_id WHERE r.order_id = $1",
			order,
		).Scan(&outboxRows); err != nil {
			t.Fatal(err)
		}
		if outboxRows != 1 {
			t.Fatalf("outbox rows = %d after a duplicate submission, want 1", outboxRows)
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
		if _, err := service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 5, Comment: "first"},
			nil,
		); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		reviewID := reviewRowID(t, ctx, sqlDB, order)
		approveReview(t, ctx, completions, sqlDB, reviewID)
		received, err := service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatalf("list received: %v", err)
		}
		if len(received.Reviews) != 1 {
			t.Fatalf("want 1 review, got %d", len(received.Reviews))
		}
		if err := service.RemoveReview(ctx, reviewID, "abuse", admin); err != nil {
			t.Fatalf("remove review: %v", err)
		}
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 0 || avg != 0 {
			t.Fatalf("removed review still counted: count=%d avg=%v, want 0 and 0", c, avg)
		}
		_, err = service.SubmitReview(
			ctx,
			reviewer,
			reviewee,
			order,
			ReviewSubmission{Rating: 3, Comment: "again"},
			nil,
		)
		assertExternal(t, err, http.StatusConflict)
		if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 0 || avg != 0 {
			t.Fatalf("rejected re-review changed stats: count=%d avg=%v, want 0 and 0", c, avg)
		}
		// The reverse direction is unaffected: the other participant can still
		// review for the same request.
		if _, err := service.SubmitReview(
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
		_, err := service.SubmitReview(
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
		_, err := service.SubmitReview(ctx, u, u, uuid.New(), ReviewSubmission{Rating: 5, Comment: "self"}, nil)
		assertExternal(t, err, http.StatusBadRequest)
	})

	t.Run("unknown reviewer is rejected", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		insertUser(t, ctx, sqlDB, reviewee)
		_, err := service.SubmitReview(ctx, reviewer, reviewee, order, ReviewSubmission{Rating: 5, Comment: "x"}, nil)
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
		if _, err := service.SubmitReview(ctx, reviewer, reviewee, orderID,
			ReviewSubmission{Rating: 4, Comment: "concurrent removal"}, nil); err != nil {
			t.Fatalf("submit review: %v", err)
		}
		reviewID := reviewRowID(t, ctx, sqlDB, orderID)

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
		}, nil, orders.UnavailableGateway{})
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

func TestReviewSubmittedOutboxIntegration(t *testing.T) {
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
	service, _ := buildReviewService(t, sqlDB)
	repo := NewPostgresRepository(reviewdb.New(sqlDB))

	t.Run("submission commits the pending review and one outbox row", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if _, err := service.SubmitReview(ctx, reviewer, reviewee, order,
			ReviewSubmission{Rating: 4, Comment: "needs moderation"}, nil); err != nil {
			t.Fatalf("submit: %v", err)
		}

		var status string
		var revision int
		var requestID uuid.NullUUID
		reviewID := reviewRowID(t, ctx, sqlDB, order)
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT moderation_status, content_revision, moderation_request_id FROM reviews WHERE id = $1", reviewID,
		).Scan(&status, &revision, &requestID); err != nil {
			t.Fatal(err)
		}
		if status != string(ModerationPending) || revision != 1 || !requestID.Valid {
			t.Fatalf("pending review state = (%q, %d, %v)", status, revision, requestID)
		}

		var eventID uuid.UUID
		var payload []byte
		var published sql.NullTime
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT event_id, payload, published_at FROM review_submitted_outbox WHERE review_id = $1", reviewID,
		).Scan(&eventID, &payload, &published); err != nil {
			t.Fatalf("read outbox row: %v", err)
		}
		if published.Valid {
			t.Fatal("outbox row published before the relay ran")
		}
		message, err := decodeSubmittedPayload(submittedPayloadVersion, payload)
		if err != nil {
			t.Fatalf("decode stored payload: %v", err)
		}
		submitted, ok := message.(*events.ReviewSubmitted)
		if !ok {
			t.Fatalf("payload type %T", message)
		}
		if submitted.GetEventId() != eventID.String() ||
			submitted.GetModerationRequestId() != requestID.UUID.String() ||
			submitted.GetReviewId() != reviewID.String() ||
			submitted.GetReviewerId() != reviewer.String() ||
			submitted.GetContentRevision() != 1 ||
			submitted.GetComment() != "needs moderation" {
			t.Fatalf("stored payload = %+v", submitted)
		}
	})

	t.Run("claims hold leases, retries reschedule and publication acknowledges", func(t *testing.T) {
		// Earlier submissions in this package leave unpublished rows; drain
		// every eligible one so the lifecycle below observes exactly the row
		// this subtest submits.
		for {
			queued, err := repo.ClaimSubmittedOutbox(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Minute))
			if err != nil {
				t.Fatalf("drain claim: %v", err)
			}
			if queued == nil {
				break
			}
			if err := repo.MarkSubmittedOutboxPublished(ctx, *queued, time.Now().UTC()); err != nil {
				t.Fatalf("drain publish: %v", err)
			}
		}

		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, sqlDB, id)
		}
		if _, err := service.SubmitReview(ctx, reviewer, reviewee, order,
			ReviewSubmission{Rating: 3, Comment: "lease lifecycle"}, nil); err != nil {
			t.Fatalf("submit: %v", err)
		}
		reviewID := reviewRowID(t, ctx, sqlDB, order)
		var eventID uuid.UUID
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT event_id FROM review_submitted_outbox WHERE review_id = $1", reviewID).Scan(&eventID); err != nil {
			t.Fatal(err)
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		firstLeaseUntil := now.Add(time.Minute)
		fact, err := repo.ClaimSubmittedOutbox(ctx, now, firstLeaseUntil)
		if err != nil || fact == nil {
			t.Fatalf("claim outbox row: fact=%+v err=%v", fact, err)
		}
		if fact.EventID != eventID || fact.ReviewID != reviewID || fact.PayloadVersion != submittedPayloadVersion ||
			fact.LeaseToken == uuid.Nil || len(fact.Payload) == 0 {
			t.Fatalf("claimed fact = %+v", fact)
		}
		if _, err := decodeSubmittedPayload(fact.PayloadVersion, fact.Payload); err != nil {
			t.Fatalf("claimed payload does not decode: %v", err)
		}
		if second, err := repo.ClaimSubmittedOutbox(
			ctx, now, firstLeaseUntil.Add(time.Minute),
		); err != nil || second != nil {
			t.Fatalf("claimed a row with a live lease: fact=%+v err=%v", second, err)
		}

		retryAt := now.Add(2 * time.Minute)
		if err := repo.ScheduleSubmittedOutboxRetry(ctx, *fact, retryAt); err != nil {
			t.Fatalf("schedule retry: %v", err)
		}
		if early, err := repo.ClaimSubmittedOutbox(
			ctx, retryAt.Add(-time.Second), retryAt.Add(time.Minute),
		); err != nil || early != nil {
			t.Fatalf("claimed before retry time: fact=%+v err=%v", early, err)
		}
		retried, err := repo.ClaimSubmittedOutbox(ctx, retryAt, retryAt.Add(time.Minute))
		if err != nil || retried == nil {
			t.Fatalf("claim retried outbox row: fact=%+v err=%v", retried, err)
		}
		if retried.EventID != eventID || retried.LeaseToken == fact.LeaseToken {
			t.Fatalf("retry changed identity or reused lease: first=%+v retry=%+v", fact, retried)
		}

		if err := repo.MarkSubmittedOutboxPublished(ctx, *fact, retryAt); err == nil {
			t.Fatal("stale lease acknowledged publication")
		}
		if err := repo.MarkSubmittedOutboxPublished(ctx, *retried, retryAt.Add(time.Second)); err != nil {
			t.Fatalf("acknowledge publication: %v", err)
		}
		if pending, err := repo.ClaimSubmittedOutbox(
			ctx, retryAt.Add(2*time.Second), retryAt.Add(3*time.Minute),
		); err != nil || pending != nil {
			t.Fatalf("published row was claimed again: fact=%+v err=%v", pending, err)
		}
		var publishedAt sql.NullTime
		if err := sqlDB.QueryRowContext(
			ctx,
			"SELECT published_at FROM review_submitted_outbox WHERE event_id = $1",
			eventID,
		).Scan(&publishedAt); err != nil {
			t.Fatal(err)
		}
		if !publishedAt.Valid {
			t.Fatal("published row retained pending state")
		}
	})
}

func approvedCompletionEvent(
	reviewID, reviewerID, requestID uuid.UUID, revision int32, generation int32,
) *moderationeventsv1.ReviewModerationFinished {
	return &moderationeventsv1.ReviewModerationFinished{
		EventId:             uuid.New().String(),
		JobId:               uuid.New().String(),
		ModerationRequestId: requestID.String(),
		SourceEventId:       uuid.New().String(),
		ReviewId:            reviewID.String(),
		ReviewerId:          reviewerID.String(),
		ContentRevision:     revision,
		RunGeneration:       generation,
		PolicyVersion:       "review-text-v1",
		Outcome:             moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
		ReasonCodes:         nil,
		FinishedAt:          timestamppb.New(time.Now().UTC()),
	}
}

func TestReviewRestorationIntegration(t *testing.T) {
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
	service, completions := buildReviewService(t, sqlDB)

	reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
		insertUser(t, ctx, sqlDB, id)
	}
	if _, err := service.SubmitReview(
		ctx, reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 5, Comment: "great"}, nil,
	); err != nil {
		t.Fatal(err)
	}
	var reviewID uuid.UUID
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM reviews WHERE reviewer_id = $1", reviewer).
		Scan(&reviewID); err != nil {
		t.Fatal(err)
	}
	approveReview(t, ctx, completions, sqlDB, reviewID)

	if err := service.RestoreReview(ctx, reviewID, "not removed", uuid.Nil, admin); err == nil {
		t.Fatal("expected restoring a live review to fail")
	}
	if err := service.RemoveReview(ctx, reviewID, "abuse", admin); err != nil {
		t.Fatal(err)
	}
	if c, _ := userStats(t, ctx, sqlDB, reviewee); c != 0 {
		t.Fatalf("after removal: count=%d, want 0", c)
	}

	if err := service.RestoreReview(ctx, reviewID, "overturned", uuid.Nil, admin); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if c, avg := userStats(t, ctx, sqlDB, reviewee); c != 1 || avg != 5 {
		t.Fatalf("after restoration: count=%d avg=%v, want 1 and 5", c, avg)
	}

	if err := service.RemoveReview(ctx, reviewID, "abuse again", admin); err != nil {
		t.Fatalf("remove a restored review again: %v", err)
	}
	if err := service.RestoreReview(ctx, reviewID, "overturned again", uuid.Nil, admin); err != nil {
		t.Fatalf("restore the second removal: %v", err)
	}
	var removals, restorations int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM review_removals WHERE review_id = $1),
		        (SELECT count(*) FROM review_restorations WHERE review_id = $1)`, reviewID,
	).Scan(&removals, &restorations); err != nil {
		t.Fatal(err)
	}
	if removals != 2 || restorations != 2 {
		t.Fatalf("removals=%d restorations=%d, want 2 and 2", removals, restorations)
	}
}

// TestListMyReviewRemovalsIntegration lists only the caller's reviews that are
// still removed outside a report, and checks the review facts Moderation
// Service reads: restored reviews and report removals are appealed elsewhere
// or not at all.
func TestListMyReviewRemovalsIntegration(t *testing.T) {
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
	service, _ := buildReviewService(t, sqlDB)

	reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
		insertUser(t, ctx, sqlDB, id)
	}
	submit := func(comment string) uuid.UUID {
		t.Helper()
		orderID := uuid.New()
		if _, err := service.SubmitReview(
			ctx, reviewer, reviewee, orderID, ReviewSubmission{Rating: 2, Comment: comment}, nil,
		); err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT id FROM reviews WHERE reviewer_id = $1 AND order_id = $2", reviewer, orderID,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Removed by an administrator outside a report: listed.
	removed := submit("removed")
	if err := service.RemoveReview(ctx, removed, "abuse", admin); err != nil {
		t.Fatal(err)
	}

	// Removed, then restored: not listed.
	restored := submit("restored")
	if err := service.RemoveReview(ctx, restored, "abuse", admin); err != nil {
		t.Fatal(err)
	}
	if err := service.RestoreReview(ctx, restored, "overturned", uuid.Nil, admin); err != nil {
		t.Fatal(err)
	}

	// Removed by a report's outcome: appealed through the report, not listed.
	byReport, reportID := submit("by report"), uuid.New()
	if err := service.RemoveReviewForReport(ctx, byReport, "report outcome", reportID, admin); err != nil {
		t.Fatal(err)
	}
	facts, err := service.ReviewFacts(ctx, byReport)
	if err != nil || !facts.Removed || facts.RemovalReportID != reportID {
		t.Fatalf("facts of a review a report removed = %+v err=%v", facts, err)
	}
	facts, err = service.ReviewFacts(ctx, restored)
	if err != nil || facts.Removed || facts.RemovalReportID != uuid.Nil {
		t.Fatalf("facts of a restored review = %+v err=%v", facts, err)
	}

	// Moderation reads one review: administrators see removed ones, the
	// reviewee sees only live ones.
	got, isRemoved, err := service.GetReview(ctx, admin, true, byReport)
	if err != nil || got.ID != byReport || !isRemoved || got.ReviewerName == "" {
		t.Fatalf("admin read of a removed review = %+v removed=%v err=%v", got, isRemoved, err)
	}
	if _, _, err := service.GetReview(ctx, reviewee, false, byReport); err == nil {
		t.Fatal("reviewee read a removed review")
	}
	got, isRemoved, err = service.GetReview(ctx, reviewee, false, restored)
	if err != nil || isRemoved || got.ID != restored {
		t.Fatalf("reviewee read of a live review = %+v removed=%v err=%v", got, isRemoved, err)
	}

	// A live review: not listed.
	submit("live")

	page, err := pagination.New(1, 20)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ListMyReviewRemovals(ctx, reviewer, page)
	if err != nil {
		t.Fatalf("list my removals: %v", err)
	}

	if len(result.Removals) != 1 || result.Removals[0].ReviewID != removed || result.Removals[0].Reason != "abuse" {
		t.Fatalf("removals = %+v, want only the review removed outside a report", result.Removals)
	}
	if result.PageInfo.TotalItems() != 1 {
		t.Fatalf("total = %d, want 1", result.PageInfo.TotalItems())
	}

	others, err := service.ListMyReviewRemovals(ctx, reviewee, page)
	if err != nil {
		t.Fatal(err)
	}
	if len(others.Removals) != 0 {
		t.Fatalf("another user's removals leaked: %+v", others.Removals)
	}
}
