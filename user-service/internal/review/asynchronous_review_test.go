package review

import (
	"context"
	"errors"
	"sync"
	"testing"

	moderationevents "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

func TestAsyncReviewConcurrentDuplicateCompletionAppliesOnce(t *testing.T) {
	stack := openModerationStack(t)
	ctx := context.Background()
	reviewer, reviewee := uuid.New(), uuid.New()
	insertUser(t, ctx, stack.sqlDB, reviewer)
	insertUser(t, ctx, stack.sqlDB, reviewee)
	id, _ := submitReview(t, ctx, stack, reviewer, reviewee, "concurrent duplicate")
	event := completionEvent(
		id,
		reviewer,
		requestIDOf(t, ctx, stack, id),
		moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
		nil,
		1,
	)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() { errs <- stack.completions.Handle(ctx, moderationMetadata(), event) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count, _ := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 {
		t.Fatalf("duplicate effects: count=%d", count)
	}
	var recorded int
	if err := stack.sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM review_moderation_processed_events WHERE event_id=$1`, event.EventId).
		Scan(&recorded); err != nil ||
		recorded != 1 {
		t.Fatalf("dedup count=%d err=%v", recorded, err)
	}
}

func TestAsyncReviewFailureAfterApplyRollsBackDedupAndPublication(t *testing.T) {
	stack := openModerationStack(t)
	ctx := context.Background()
	reviewer, reviewee := uuid.New(), uuid.New()
	insertUser(t, ctx, stack.sqlDB, reviewer)
	insertUser(t, ctx, stack.sqlDB, reviewee)
	id, _ := submitReview(t, ctx, stack, reviewer, reviewee, "retry after rollback")
	event := completionEvent(
		id,
		reviewer,
		requestIDOf(t, ctx, stack, id),
		moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
		nil,
		1,
	)
	repo := NewPostgresRepository(reviewdb.New(stack.sqlDB))
	boom := errors.New("simulated commit-window failure")
	handler := NewCompletionHandler(repo, func(ctx context.Context, op func(*reviewdb.Queries) error) error {
		tx, err := stack.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := op(reviewdb.New(tx)); err != nil {
			return err
		}
		return boom
	})
	if err := handler.Handle(ctx, moderationMetadata(), event); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	status, _, generation := reviewModerationState(t, ctx, stack, id)
	if status != "pending_moderation" || generation != 0 {
		t.Fatalf("rollback published: %s/%d", status, generation)
	}
	var recorded int
	if err := stack.sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM review_moderation_processed_events WHERE event_id=$1`, event.EventId).
		Scan(&recorded); err != nil ||
		recorded != 0 {
		t.Fatalf("rollback retained dedup: %d,%v", recorded, err)
	}
	if err := stack.completions.Handle(ctx, moderationMetadata(), event); err != nil {
		t.Fatal(err)
	}
	if count, _ := userStats(t, ctx, stack.sqlDB, reviewee); count != 1 {
		t.Fatalf("retry failed to apply exactly once: %d", count)
	}
}

func TestInvalidCompletionReasonsCannotRejectOrConsumeEvent(t *testing.T) {
	stack := openModerationStack(t)
	ctx := context.Background()
	reviewer, reviewee := uuid.New(), uuid.New()
	insertUser(t, ctx, stack.sqlDB, reviewer)
	insertUser(t, ctx, stack.sqlDB, reviewee)
	id, _ := submitReview(t, ctx, stack, reviewer, reviewee, "unknown reason")
	event := completionEvent(
		id,
		reviewer,
		requestIDOf(t, ctx, stack, id),
		moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED,
		[]string{"NOT_A_POLICY_REASON", "NOT_A_POLICY_REASON"},
		1,
	)
	for _, reasons := range [][]string{{"NOT_A_POLICY_REASON"}, {"SPAM", "SPAM"}, {"SPAM", "SPAM", "SPAM", "SPAM", "SPAM", "SPAM"}} {
		event.ReasonCodes = reasons
		if err := stack.completions.Handle(ctx, moderationMetadata(), event); err != nil {
			t.Fatal(err)
		}
		status, codes, _ := reviewModerationState(t, ctx, stack, id)
		if status != "pending_moderation" || len(codes) != 0 {
			t.Fatalf("did not reproduce: %s,%v", status, codes)
		}
		var n int
		if err := stack.sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM review_moderation_processed_events WHERE event_id=$1`, event.EventId).
			Scan(&n); err != nil ||
			n != 0 {
			t.Fatal("invalid rejection consumed dedup identity")
		}
	}
	event.ReasonCodes = []string{"SPAM"}
	if err := stack.completions.Handle(ctx, moderationMetadata(), event); err != nil {
		t.Fatal(err)
	}
}

func TestLaterGenerationCannotReplaceContentVerdict(t *testing.T) {
	stack := openModerationStack(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name         string
		firstOutcome moderationevents.ModerationOutcomeType
		firstCodes   []string
		lateOutcome  moderationevents.ModerationOutcomeType
		lateCodes    []string
		wantStatus   string
		wantCodes    []string
	}{
		{
			name:         "approval remains final",
			firstOutcome: moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
			lateOutcome:  moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED,
			lateCodes:    []string{"SPAM"},
			wantStatus:   string(ModerationApproved),
		},
		{
			name:         "rejection remains final",
			firstOutcome: moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED,
			firstCodes:   []string{"HATE"},
			lateOutcome:  moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
			wantStatus:   string(ModerationRejected),
			wantCodes:    []string{"HATE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewer, reviewee := uuid.New(), uuid.New()
			insertUser(t, ctx, stack.sqlDB, reviewer)
			insertUser(t, ctx, stack.sqlDB, reviewee)
			id, _ := submitReview(t, ctx, stack, reviewer, reviewee, tc.name)
			requestID := requestIDOf(t, ctx, stack, id)
			first := completionEvent(id, reviewer, requestID, tc.firstOutcome, tc.firstCodes, 1)
			if err := stack.completions.Handle(ctx, moderationMetadata(), first); err != nil {
				t.Fatalf("apply first result: %v", err)
			}

			late := completionEvent(id, reviewer, requestID, tc.lateOutcome, tc.lateCodes, 2)
			if err := stack.completions.Handle(ctx, moderationMetadata(), late); err != nil {
				t.Fatalf("acknowledge later result: %v", err)
			}
			status, codes, generation := reviewModerationState(t, ctx, stack, id)
			if status != tc.wantStatus || generation != 1 || len(codes) != len(tc.wantCodes) {
				t.Fatalf("saved result changed: (%q, %v, %d)", status, codes, generation)
			}
			for i := range codes {
				if codes[i] != tc.wantCodes[i] {
					t.Fatalf("saved reason codes changed: %v, want %v", codes, tc.wantCodes)
				}
			}
			var wantCount int64
			if tc.firstOutcome == moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED {
				wantCount = 1
			}
			if count, _ := userStats(t, ctx, stack.sqlDB, reviewee); count != wantCount {
				t.Fatalf("late result changed published statistics: got %d, want %d", count, wantCount)
			}
		})
	}
}
