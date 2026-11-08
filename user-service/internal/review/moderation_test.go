package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	moderationeventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeModerationStore records the consumer's persistence calls and lets a test
// pin what the database returns at each step of the completion transaction.
type fakeModerationStore struct {
	// scripted outcomes
	duplicate    bool
	processedErr error
	lockErr      error
	target       ModerationTarget
	targetErr    error
	applyErr     error
	recalcErr    error

	// observed calls, in order
	calls          []string
	processedFor   []uuid.UUID
	locked         []uuid.UUID
	readFor        []uuid.UUID
	appliedFor     []uuid.UUID
	appliedOutcome ModerationCompletion
	recalculated   []uuid.UUID
}

func (f *fakeModerationStore) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeModerationStore) RecordModerationProcessedEvent(
	_ context.Context, _ *reviewdb.Queries, eventID uuid.UUID,
) (int64, error) {
	f.record("record")
	f.processedFor = append(f.processedFor, eventID)
	if f.processedErr != nil {
		return 0, f.processedErr
	}
	if f.duplicate {
		return 0, nil
	}
	return 1, nil
}

func (f *fakeModerationStore) LockReviewForModeration(
	_ context.Context, _ *reviewdb.Queries, reviewID uuid.UUID,
) error {
	f.record("lock")
	f.locked = append(f.locked, reviewID)
	return f.lockErr
}

func (f *fakeModerationStore) GetReviewForModeration(
	_ context.Context, _ *reviewdb.Queries, reviewID uuid.UUID,
) (ModerationTarget, error) {
	f.record("read")
	f.readFor = append(f.readFor, reviewID)
	if f.targetErr != nil {
		return ModerationTarget{}, f.targetErr
	}
	return f.target, nil
}

func (f *fakeModerationStore) ApplyModerationOutcome(
	_ context.Context, _ *reviewdb.Queries, reviewID uuid.UUID, completion ModerationCompletion,
) error {
	f.record("apply")
	f.appliedFor = append(f.appliedFor, reviewID)
	f.appliedOutcome = completion
	return f.applyErr
}

func (f *fakeModerationStore) RecalculateStats(
	_ context.Context, _ *reviewdb.Queries, revieweeID uuid.UUID,
) error {
	f.record("recalc")
	f.recalculated = append(f.recalculated, revieweeID)
	return f.recalcErr
}

func (f *fakeModerationStore) assertOrder(t *testing.T, want ...string) {
	t.Helper()
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	for i, call := range want {
		if f.calls[i] != call {
			t.Fatalf("calls = %v, want %v", f.calls, want)
		}
	}
}

func newTestCompletionHandler(store ModerationStore) *CompletionHandler {
	return NewCompletionHandler(store, func(_ context.Context, operation func(*reviewdb.Queries) error) error {
		return operation(reviewdb.New(nil))
	})
}

func finishedEvent(
	mutate func(*moderationeventsv1.ReviewModerationFinished),
) *moderationeventsv1.ReviewModerationFinished {
	event := &moderationeventsv1.ReviewModerationFinished{
		EventId:             uuid.New().String(),
		JobId:               uuid.New().String(),
		ModerationRequestId: uuid.New().String(),
		SourceEventId:       uuid.New().String(),
		ReviewId:            uuid.New().String(),
		ReviewerId:          uuid.New().String(),
		ContentRevision:     1,
		RunGeneration:       1,
		PolicyVersion:       "review-text-v1",
		ConfigVersionId:     "cfg-2026-11-01",
		Outcome:             moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED,
		FinishedAt:          timestamppb.New(time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)),
	}
	if mutate != nil {
		mutate(event)
	}
	return event
}

func pendingTarget(event *moderationeventsv1.ReviewModerationFinished) ModerationTarget {
	requestID, _ := uuid.Parse(event.GetModerationRequestId())
	reviewerID, _ := uuid.Parse(event.GetReviewerId())
	reviewID, _ := uuid.Parse(event.GetReviewId())
	return ModerationTarget{
		ID:                   reviewID,
		ReviewerID:           reviewerID,
		RevieweeID:           uuid.New(),
		ModerationStatus:     ModerationPending,
		ModerationRequestID:  requestID,
		ContentRevision:      1,
		AppliedRunGeneration: 0,
	}
}

func TestCompletionHandlerAppliesApprovedOutcome(t *testing.T) {
	event := finishedEvent(nil)
	store := &fakeModerationStore{target: pendingTarget(event)}
	handler := newTestCompletionHandler(store)

	if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	store.assertOrder(t, "record", "lock", "read", "apply", "recalc")
	if store.appliedFor[0] != store.target.ID {
		t.Fatalf("applied to %v, want %v", store.appliedFor[0], store.target.ID)
	}
	outcome := store.appliedOutcome
	if outcome.Status != ModerationApproved {
		t.Fatalf("status = %q, want approved", outcome.Status)
	}
	if len(outcome.ReasonCodes) != 0 {
		t.Fatalf("approved review must carry no reason codes: %v", outcome.ReasonCodes)
	}
	if outcome.RunGeneration != 1 || outcome.PolicyVersion != "review-text-v1" ||
		outcome.ConfigVersionID != "cfg-2026-11-01" {
		t.Fatalf("unexpected outcome metadata: %+v", outcome)
	}
	if !outcome.ModeratedAt.Equal(time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("moderated_at should come from finished_at: %v", outcome.ModeratedAt)
	}
	if store.recalculated[0] != store.target.RevieweeID {
		t.Fatalf("stats recalculated for %v, want the reviewee %v", store.recalculated[0], store.target.RevieweeID)
	}
}

func TestCompletionHandlerAppliesRejectedOutcome(t *testing.T) {
	event := finishedEvent(func(e *moderationeventsv1.ReviewModerationFinished) {
		e.Outcome = moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED
		e.ReasonCodes = []string{"hate", "harassment"}
	})
	store := &fakeModerationStore{target: pendingTarget(event)}
	handler := newTestCompletionHandler(store)

	if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	store.assertOrder(t, "record", "lock", "read", "apply", "recalc")
	if store.appliedOutcome.Status != ModerationRejected {
		t.Fatalf("status = %q, want rejected", store.appliedOutcome.Status)
	}
	if len(store.appliedOutcome.ReasonCodes) != 2 {
		t.Fatalf("reason codes not carried: %v", store.appliedOutcome.ReasonCodes)
	}
}

func TestCompletionHandlerAppliesErrorOutcome(t *testing.T) {
	event := finishedEvent(func(e *moderationeventsv1.ReviewModerationFinished) {
		e.Outcome = moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
		e.PolicyVersion = ""
		e.ConfigVersionId = ""
	})
	store := &fakeModerationStore{target: pendingTarget(event)}
	handler := newTestCompletionHandler(store)

	if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	store.assertOrder(t, "record", "lock", "read", "apply", "recalc")
	if store.appliedOutcome.Status != ModerationError {
		t.Fatalf("status = %q, want moderation_error", store.appliedOutcome.Status)
	}
}

func TestCompletionHandlerNoOps(t *testing.T) {
	t.Run("duplicate event is a success no-op", func(t *testing.T) {
		event := finishedEvent(nil)
		store := &fakeModerationStore{duplicate: true, target: pendingTarget(event)}
		handler := newTestCompletionHandler(store)
		if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
			t.Fatalf("duplicate event must be acknowledged: %v", err)
		}
		store.assertOrder(t, "record")
	})

	t.Run("missing review is a success no-op", func(t *testing.T) {
		event := finishedEvent(nil)
		store := &fakeModerationStore{targetErr: errReviewNotFound}
		handler := newTestCompletionHandler(store)
		if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
			t.Fatalf("missing review must be acknowledged: %v", err)
		}
		store.assertOrder(t, "record", "lock", "read")
	})

	t.Run("deleted review is a success no-op", func(t *testing.T) {
		event := finishedEvent(nil)
		target := pendingTarget(event)
		target.Deleted = true
		store := &fakeModerationStore{target: target}
		handler := newTestCompletionHandler(store)
		if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
			t.Fatalf("deleted review must be acknowledged: %v", err)
		}
		store.assertOrder(t, "record", "lock", "read")
	})

	for _, tc := range []struct {
		name   string
		mutate func(*moderationeventsv1.ReviewModerationFinished)
	}{
		{"different reviewer", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ReviewerId = uuid.New().String()
		}},
		{"different moderation request", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ModerationRequestId = uuid.New().String()
		}},
		{"different content revision", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ContentRevision = 2
		}},
	} {
		t.Run("identity mismatch from "+tc.name+" never applies", func(t *testing.T) {
			event := finishedEvent(nil)
			target := pendingTarget(event)
			// The saved review keeps the original identity; the event diverges.
			tc.mutate(event)
			store := &fakeModerationStore{target: target}
			handler := newTestCompletionHandler(store)
			if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatalf("mismatch must be acknowledged: %v", err)
			}
			store.assertOrder(t, "record", "lock", "read")
		})
	}

	t.Run("superseded generation never applies", func(t *testing.T) {
		event := finishedEvent(func(e *moderationeventsv1.ReviewModerationFinished) { e.RunGeneration = 1 })
		target := pendingTarget(event)
		target.AppliedRunGeneration = 2
		store := &fakeModerationStore{target: target}
		handler := newTestCompletionHandler(store)
		if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
			t.Fatalf("superseded event must be acknowledged: %v", err)
		}
		store.assertOrder(t, "record", "lock", "read")
	})

	t.Run("equal generation never applies", func(t *testing.T) {
		event := finishedEvent(nil)
		target := pendingTarget(event)
		target.AppliedRunGeneration = 1
		store := &fakeModerationStore{target: target}
		handler := newTestCompletionHandler(store)
		if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
			t.Fatalf("replayed event must be acknowledged: %v", err)
		}
		store.assertOrder(t, "record", "lock", "read")
	})
}

func TestCompletionHandlerRejectsInvalidEventsWithoutTouchingTheStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*moderationeventsv1.ReviewModerationFinished)
	}{
		{"unspecified outcome", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.Outcome = moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_UNSPECIFIED
		}},
		{"unknown outcome value", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.Outcome = moderationeventsv1.ModerationOutcomeType(9)
		}},
		{"rejected without reason codes", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.Outcome = moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED
		}},
		{"approved with reason codes", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ReasonCodes = []string{"hate"}
		}},
		{"error with reason codes", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.Outcome = moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
			e.ReasonCodes = []string{"hate"}
		}},
		{"invalid event id", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.EventId = "not-a-uuid"
		}},
		{"invalid review id", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ReviewId = "not-a-uuid"
		}},
		{"invalid moderation request id", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ModerationRequestId = "not-a-uuid"
		}},
		{"invalid reviewer id", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ReviewerId = "not-a-uuid"
		}},
		{"zero content revision", func(e *moderationeventsv1.ReviewModerationFinished) {
			e.ContentRevision = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := finishedEvent(tc.mutate)
			store := &fakeModerationStore{target: pendingTarget(event)}
			handler := newTestCompletionHandler(store)
			if err := handler.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatalf("invalid event must be acknowledged without applying: %v", err)
			}
			if len(store.calls) != 0 {
				t.Fatalf("invalid event touched the store: %v", store.calls)
			}
		})
	}
}

func TestCompletionHandlerPropagatesTransientFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		store *fakeModerationStore
	}{
		{"dedup insert fails", &fakeModerationStore{processedErr: boom}},
		{"lock fails", &fakeModerationStore{lockErr: boom}},
		{"read fails", &fakeModerationStore{targetErr: boom}},
		{"apply fails", &fakeModerationStore{applyErr: boom}},
		{"recalculation fails", &fakeModerationStore{recalcErr: boom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := finishedEvent(nil)
			tc.store.target = pendingTarget(event)
			handler := newTestCompletionHandler(tc.store)
			if err := handler.Handle(context.Background(), kafka.Metadata{}, event); !errors.Is(err, boom) {
				t.Fatalf("transient failure must return an error so the offset stays uncommitted: %v", err)
			}
		})
	}
}

func TestModerationCompletionFromEventFallsBackToNow(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	event := finishedEvent(func(e *moderationeventsv1.ReviewModerationFinished) { e.FinishedAt = nil })
	completion, ok := moderationCompletionFromEvent(event, now)
	if !ok {
		t.Fatal("event without finished_at should still be valid")
	}
	if !completion.ModeratedAt.Equal(now) {
		t.Fatalf("moderated_at = %v, want the fallback now %v", completion.ModeratedAt, now)
	}
}
