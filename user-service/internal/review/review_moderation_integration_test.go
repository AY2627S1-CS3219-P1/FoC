package review

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	moderationeventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// moderationIntegrationStack bundles what completion tests need: the service,
// the completion handler and raw database access.
type moderationIntegrationStack struct {
	service     *Service
	completions *CompletionHandler
	sqlDB       *sql.DB
}

func openModerationStack(t *testing.T) moderationIntegrationStack {
	t.Helper()
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
	service, completions := buildReviewService(t, sqlDB)
	return moderationIntegrationStack{service: service, completions: completions, sqlDB: sqlDB}
}

// submitReview submits a review and returns the review and order ids.
func submitReview(
	t *testing.T,
	ctx context.Context,
	stack moderationIntegrationStack,
	reviewerID, revieweeID uuid.UUID,
	comment string,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orderID := uuid.New()
	if _, err := stack.service.SubmitReview(ctx, reviewerID, revieweeID, orderID,
		ReviewSubmission{Rating: 3, Comment: comment}); err != nil {
		t.Fatalf("submit review: %v", err)
	}
	return reviewRowID(t, ctx, stack.sqlDB, orderID), orderID
}

// reviewModerationState reads a review's status, reason codes and applied
// generation directly from the database.
func reviewModerationState(
	t *testing.T,
	ctx context.Context,
	stack moderationIntegrationStack,
	reviewID uuid.UUID,
) (string, []string, int32) {
	t.Helper()
	var status string
	var codes []byte
	var generation int32
	if err := stack.sqlDB.QueryRowContext(ctx,
		"SELECT moderation_status, reason_codes, applied_run_generation FROM reviews WHERE id = $1", reviewID,
	).Scan(&status, &codes, &generation); err != nil {
		t.Fatalf("read review %v: %v", reviewID, err)
	}
	parsed, err := submittedReasonCodes(codes)
	if err != nil {
		t.Fatal(err)
	}
	return status, parsed, generation
}

func processedEventCount(t *testing.T, ctx context.Context, stack moderationIntegrationStack) int64 {
	t.Helper()
	var count int64
	if err := stack.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM review_moderation_processed_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func completionEvent(
	reviewID, reviewerID, requestID uuid.UUID,
	outcome moderationeventsv1.ModerationOutcomeType,
	codes []string,
	generation int32,
) *moderationeventsv1.ReviewModerationFinished {
	return &moderationeventsv1.ReviewModerationFinished{
		EventId:             uuid.New().String(),
		JobId:               uuid.New().String(),
		ModerationRequestId: requestID.String(),
		SourceEventId:       uuid.New().String(),
		ReviewId:            reviewID.String(),
		ReviewerId:          reviewerID.String(),
		ContentRevision:     1,
		RunGeneration:       generation,
		PolicyVersion:       "review-text-v1",
		ConfigVersionId:     "cfg-test",
		Outcome:             outcome,
		ReasonCodes:         codes,
		FinishedAt:          timestamppb.New(time.Now().UTC()),
	}
}

// applyCompletion sends one completion event through the handler.
func applyCompletion(
	t *testing.T,
	ctx context.Context,
	stack moderationIntegrationStack,
	event *moderationeventsv1.ReviewModerationFinished,
) error {
	t.Helper()
	return stack.completions.Handle(ctx, moderationMetadata(), event)
}

func requestIDOf(t *testing.T, ctx context.Context, stack moderationIntegrationStack, reviewID uuid.UUID) uuid.UUID {
	t.Helper()
	var requestID uuid.NullUUID
	if err := stack.sqlDB.QueryRowContext(ctx,
		"SELECT moderation_request_id FROM reviews WHERE id = $1", reviewID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if !requestID.Valid {
		t.Fatalf("review %v has no moderation request id", reviewID)
	}
	return requestID.UUID
}

func TestReviewModerationIntegration(t *testing.T) {
	stack := openModerationStack(t)
	ctx := context.Background()

	// reviewCompletionRequest bundles the identifiers a completion event needs.
	type reviewCompletionRequest struct {
		reviewID   uuid.UUID
		reviewerID uuid.UUID
		requestID  uuid.UUID
	}

	prepare := func(t *testing.T, reviewerID, revieweeID uuid.UUID, comment string) reviewCompletionRequest {
		t.Helper()
		reviewID, _ := submitReview(t, ctx, stack, reviewerID, revieweeID, comment)
		return reviewCompletionRequest{
			reviewID:   reviewID,
			reviewerID: reviewerID,
			requestID:  requestIDOf(t, ctx, stack, reviewID),
		}
	}

	t.Run("historical reviews stay published while new ones wait for moderation", func(t *testing.T) {
		reviewer1, reviewer2, reviewee := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer1, reviewer2, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}

		// A row that existed before the moderation migration: approved by the
		// column default, no moderation request, revision 1.
		historicalOrder := uuid.New()
		if _, err := stack.sqlDB.ExecContext(ctx,
			`INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment)
			 VALUES ($1, $2, $3, $4, 5, 'historical')`,
			uuid.New(), reviewer1, reviewee, historicalOrder); err != nil {
			t.Fatal(err)
		}

		pending := prepare(t, reviewer2, reviewee, "brand new")

		received, err := stack.service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(received.Reviews) != 1 || received.Reviews[0].ReviewerID != reviewer1 {
			t.Fatalf("pending review visible to the reviewee: %+v", received.Reviews)
		}
		if received.TotalItems() != 1 {
			t.Fatalf("approved count = %d, want 1", received.TotalItems())
		}
		given, err := stack.service.ListReviewsFromUser(ctx, reviewer2, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(given.Reviews) != 0 {
			t.Fatalf("pending review visible in the author's given list: %+v", given.Reviews)
		}

		// The recalculation only counts the historical review.
		recalcNow(t, ctx, stack.sqlDB, reviewee)
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 || avg != 5 {
			t.Fatalf("stats = (%d, %v), want (1, 5)", count, avg)
		}

		approveReview(t, ctx, stack.completions, stack.sqlDB, pending.reviewID)
		received, err = stack.service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(received.Reviews) != 2 {
			t.Fatalf("approved review not published: %+v", received.Reviews)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 2 || avg != 4 {
			t.Fatalf("stats after approval = (%d, %v), want (2, 4)", count, avg)
		}
	})

	t.Run("approval commits once and replays do not double count", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}
		pending := prepare(t, reviewer, reviewee, "replay me")
		event := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, nil, 1)

		if err := applyCompletion(t, ctx, stack, event); err != nil {
			t.Fatalf("apply approval: %v", err)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 || avg != 3 {
			t.Fatalf("stats after approval = (%d, %v), want (1, 3)", count, avg)
		}
		before := processedEventCount(t, ctx, stack)

		// Redelivery of the same event is deduplicated.
		if err := applyCompletion(t, ctx, stack, event); err != nil {
			t.Fatalf("replayed event must be acknowledged: %v", err)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 || avg != 3 {
			t.Fatalf("replay changed stats: (%d, %v)", count, avg)
		}
		if after := processedEventCount(t, ctx, stack); after != before {
			t.Fatalf("replay recorded another processed event: %d -> %d", before, after)
		}
	})

	t.Run("rejected and error outcomes stay unpublished", func(t *testing.T) {
		reviewer1, reviewer2, reviewee := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer1, reviewer2, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}

		rejected := prepare(t, reviewer1, reviewee, "reject me")
		rejectedEvent := completionEvent(rejected.reviewID, rejected.reviewerID, rejected.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED, []string{"hate"}, 1)
		if err := applyCompletion(t, ctx, stack, rejectedEvent); err != nil {
			t.Fatalf("apply rejection: %v", err)
		}
		status, codes, generation := reviewModerationState(t, ctx, stack, rejected.reviewID)
		if status != string(ModerationRejected) || len(codes) != 1 || codes[0] != "hate" || generation != 1 {
			t.Fatalf("rejected state = (%q, %v, %d)", status, codes, generation)
		}

		errored := prepare(t, reviewer2, reviewee, "error me")
		erroredEvent := completionEvent(errored.reviewID, errored.reviewerID, errored.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR, nil, 1)
		if err := applyCompletion(t, ctx, stack, erroredEvent); err != nil {
			t.Fatalf("apply error: %v", err)
		}
		status, codes, _ = reviewModerationState(t, ctx, stack, errored.reviewID)
		if status != string(ModerationError) || len(codes) != 0 {
			t.Fatalf("error state = (%q, %v)", status, codes)
		}

		// Neither review is published to the reviewee or counted in stats.
		received, err := stack.service.ListReviewsForUser(ctx, reviewee, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(received.Reviews) != 0 {
			t.Fatalf("unpublished reviews visible to the reviewee: %+v", received.Reviews)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 0 || avg != 0 {
			t.Fatalf("stats = (%d, %v), want (0, 0)", count, avg)
		}

		// The author still sees both with their statuses and reason codes.
		list, err := stack.service.ListUnpublishedReviewsForUser(
			ctx, reviewer1, UnpublishedFilter{}, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Reviews) != 1 || list.Reviews[0].ModerationStatus != ModerationRejected ||
			len(list.Reviews[0].ModerationReasonCodes) != 1 || list.Reviews[0].ModerationReasonCodes[0] != "hate" ||
			list.Reviews[0].ModeratedAt.IsZero() || list.Reviews[0].AppliedRunGeneration != 1 {
			t.Fatalf("unpublished list = %+v", list.Reviews)
		}
	})

	t.Run("invalid outcomes never apply or record", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}
		pending := prepare(t, reviewer, reviewee, "invalid outcomes")
		before := processedEventCount(t, ctx, stack)

		for _, tc := range []struct {
			name    string
			outcome moderationeventsv1.ModerationOutcomeType
			codes   []string
		}{
			{"unspecified", moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_UNSPECIFIED, nil},
			{"rejected without codes", moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED, nil},
			{"approved with codes", moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, []string{"hate"}},
			{"error with codes", moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR, []string{"hate"}},
		} {
			event := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID, tc.outcome, tc.codes, 1)
			if err := applyCompletion(t, ctx, stack, event); err != nil {
				t.Fatalf("%s must be acknowledged: %v", tc.name, err)
			}
		}

		status, codes, generation := reviewModerationState(t, ctx, stack, pending.reviewID)
		if status != string(ModerationPending) || len(codes) != 0 || generation != 0 {
			t.Fatalf("invalid outcomes changed the review: (%q, %v, %d)", status, codes, generation)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 0 || avg != 0 {
			t.Fatalf("invalid outcomes changed stats: (%d, %v)", count, avg)
		}
		if after := processedEventCount(t, ctx, stack); after != before {
			t.Fatalf("invalid outcomes recorded processed events: %d -> %d", before, after)
		}
	})

	t.Run("a completion for another request never applies", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}
		pending := prepare(t, reviewer, reviewee, "mismatched")
		before := processedEventCount(t, ctx, stack)

		event := completionEvent(pending.reviewID, pending.reviewerID, uuid.New(),
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, nil, 1)
		if err := applyCompletion(t, ctx, stack, event); err != nil {
			t.Fatalf("mismatched completion must be acknowledged: %v", err)
		}
		status, _, _ := reviewModerationState(t, ctx, stack, pending.reviewID)
		if status != string(ModerationPending) {
			t.Fatalf("mismatched completion applied: %q", status)
		}
		// The event was processed and decided: it is recorded so a redelivery
		// short-circuits.
		if after := processedEventCount(t, ctx, stack); after != before+1 {
			t.Fatalf("mismatched completion not recorded: %d -> %d", before, after)
		}
	})

	t.Run("generations guard against stale verdicts", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}
		pending := prepare(t, reviewer, reviewee, "generations")

		errored := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR, nil, 1)
		if err := applyCompletion(t, ctx, stack, errored); err != nil {
			t.Fatalf("apply error generation 1: %v", err)
		}
		status, _, generation := reviewModerationState(t, ctx, stack, pending.reviewID)
		if status != string(ModerationError) || generation != 1 {
			t.Fatalf("state = (%q, %d), want error generation 1", status, generation)
		}

		// The same generation replayed under a new event id cannot overwrite;
		// neither can a stale generation 0.
		for _, staleGeneration := range []int32{1, 0} {
			stale := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID,
				moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, nil, staleGeneration)
			if err := applyCompletion(t, ctx, stack, stale); err != nil {
				t.Fatalf("stale generation %d must be acknowledged: %v", staleGeneration, err)
			}
			status, _, applied := reviewModerationState(t, ctx, stack, pending.reviewID)
			if status != string(ModerationError) || applied != 1 {
				t.Fatalf("stale generation %d applied: (%q, %d)", staleGeneration, status, applied)
			}
		}

		// An explicit retry moves the verdict forward.
		retried := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, nil, 2)
		if err := applyCompletion(t, ctx, stack, retried); err != nil {
			t.Fatalf("apply generation 2: %v", err)
		}
		status, codes, generation := reviewModerationState(t, ctx, stack, pending.reviewID)
		if status != string(ModerationApproved) || len(codes) != 0 || generation != 2 {
			t.Fatalf("retried state = (%q, %v, %d), want approved generation 2", status, codes, generation)
		}
		if count, avg := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 || avg != 3 {
			t.Fatalf("stats after retried approval = (%d, %v), want (1, 3)", count, avg)
		}
	})

	t.Run("a removed review ignores completions", func(t *testing.T) {
		reviewer, reviewee, admin := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee, admin} {
			insertUser(t, ctx, stack.sqlDB, id)
		}
		pending := prepare(t, reviewer, reviewee, "removed before completion")
		if err := stack.service.RemoveReview(ctx, pending.reviewID, "abuse", admin); err != nil {
			t.Fatalf("remove review: %v", err)
		}

		approved := completionEvent(pending.reviewID, pending.reviewerID, pending.requestID,
			moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED, nil, 1)
		if err := applyCompletion(t, ctx, stack, approved); err != nil {
			t.Fatalf("completion for a removed review must be acknowledged: %v", err)
		}
		var status string
		var deletedAt sql.NullTime
		if err := stack.sqlDB.QueryRowContext(ctx,
			"SELECT moderation_status, deleted_at FROM reviews WHERE id = $1", pending.reviewID,
		).Scan(&status, &deletedAt); err != nil {
			t.Fatal(err)
		}
		if status != string(ModerationPending) || !deletedAt.Valid {
			t.Fatalf("completion resurrected the removed review: (%q, deleted=%t)", status, deletedAt.Valid)
		}
	})

	t.Run("unpublished list filters and pagination totals align", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{reviewer, reviewee} {
			insertUser(t, ctx, stack.sqlDB, id)
		}

		pendingReview, _ := submitReview(t, ctx, stack, reviewer, reviewee, "still pending")
		rejectedReview, rejectedOrder := submitReview(t, ctx, stack, reviewer, reviewee, "rejected")
		rejected := reviewCompletionRequest{
			reviewID:   rejectedReview,
			reviewerID: reviewer,
			requestID:  requestIDOf(t, ctx, stack, rejectedReview),
		}
		if err := applyCompletion(
			t,
			ctx,
			stack,
			completionEvent(
				rejected.reviewID,
				rejected.reviewerID,
				rejected.requestID,
				moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED,
				[]string{"spam"},
				1,
			),
		); err != nil {
			t.Fatal(err)
		}
		erroredReview, _ := submitReview(t, ctx, stack, reviewer, reviewee, "errored")
		errored := reviewCompletionRequest{
			reviewID:   erroredReview,
			reviewerID: reviewer,
			requestID:  requestIDOf(t, ctx, stack, erroredReview),
		}
		if err := applyCompletion(
			t,
			ctx,
			stack,
			completionEvent(errored.reviewID, errored.reviewerID, errored.requestID,
				moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR, nil, 1),
		); err != nil {
			t.Fatal(err)
		}
		approvedReview, _ := submitReview(t, ctx, stack, reviewer, reviewee, "approved")
		approveReview(t, ctx, stack.completions, stack.sqlDB, approvedReview)

		unpublished, err := stack.service.ListUnpublishedReviewsForUser(
			ctx, reviewer, UnpublishedFilter{}, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if unpublished.TotalItems() != 3 {
			t.Fatalf("total = %d, want the three unpublished reviews", unpublished.TotalItems())
		}
		// Newest first; the approved review is never in the collection.
		if len(unpublished.Reviews) != 3 || unpublished.Reviews[0].ID != erroredReview {
			t.Fatalf("unexpected order: %+v", unpublished.Reviews)
		}

		pendingStatus := ModerationPending
		filtered, err := stack.service.ListUnpublishedReviewsForUser(
			ctx, reviewer, UnpublishedFilter{Status: &pendingStatus}, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if filtered.TotalItems() != 1 || len(filtered.Reviews) != 1 || filtered.Reviews[0].ID != pendingReview {
			t.Fatalf("pending filter = %+v", filtered)
		}

		byOrder, err := stack.service.ListUnpublishedReviewsForUser(
			ctx, reviewer, UnpublishedFilter{OrderID: &rejectedOrder}, mustPage(t, 1, 20))
		if err != nil {
			t.Fatal(err)
		}
		if byOrder.TotalItems() != 1 || len(byOrder.Reviews) != 1 || byOrder.Reviews[0].ID != rejectedReview {
			t.Fatalf("order filter = %+v", byOrder)
		}

		paged, err := stack.service.ListUnpublishedReviewsForUser(
			ctx, reviewer, UnpublishedFilter{}, mustPage(t, 2, 2))
		if err != nil {
			t.Fatal(err)
		}
		if paged.TotalItems() != 3 || len(paged.Reviews) != 1 || paged.Reviews[0].ID != pendingReview {
			t.Fatalf("second page = total %d reviews %+v", paged.TotalItems(), paged.Reviews)
		}
		// The list joins both participants' display names.
		if paged.Reviews[0].ReviewerName == "" || paged.Reviews[0].RevieweeName == "" {
			t.Fatalf("display names not joined: %+v", paged.Reviews[0])
		}
	})
}

// recalcNow runs the approved-only statistics recalculation in its own
// transaction, as the service would.
func recalcNow(t *testing.T, ctx context.Context, sqlDB *sql.DB, revieweeID uuid.UUID) {
	t.Helper()
	repo := NewPostgresRepository(reviewdb.New(sqlDB))
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := repo.RecalculateStats(ctx, reviewdb.New(tx), revieweeID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
