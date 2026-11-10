// Applies review moderation completions from the content moderation service.
// Consumption is at least once, so every decision is made in one transaction:
// record the event, lock and read the review, verify the submission identity
// and apply the verdict. Anything that does not apply returns success so the
// offset is committed; only transient database failures return an error.
package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	moderationeventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// ModerationFinishedTopic carries moderation jobs' terminal results, keyed by
// the review UUID as UTF-8 text.
const ModerationFinishedTopic = "moderation.review_moderation_finished"

// CompletionHandler applies moderation outcomes to the saved reviews.
type CompletionHandler struct {
	store           ModerationStore
	withTransaction WithTransaction
	now             func() time.Time
}

func NewCompletionHandler(store ModerationStore, withTransaction WithTransaction) *CompletionHandler {
	return &CompletionHandler{store: store, withTransaction: withTransaction, now: time.Now}
}

// Handle applies one ReviewModerationFinished event. An invalid outcome or an
// outcome that no longer matches the saved review is acknowledged without
// applying anything; a database failure returns an error so the offset stays
// uncommitted and the delivery is retried.
func (h *CompletionHandler) Handle(
	ctx context.Context,
	meta kafka.Metadata,
	event *moderationeventsv1.ReviewModerationFinished,
) error {
	log := slog.With(
		"topic", meta.Topic,
		"partition", meta.Partition,
		"offset", meta.Offset,
		"event_id", event.GetEventId(),
		"review_id", event.GetReviewId(),
	)

	eventID, err := uuid.Parse(event.GetEventId())
	if err != nil || eventID == uuid.Nil {
		log.WarnContext(ctx, "moderation completion has an invalid event id; acknowledged without applying")
		return nil
	}
	reviewID, err := uuid.Parse(event.GetReviewId())
	if err != nil || reviewID == uuid.Nil {
		log.WarnContext(ctx, "moderation completion has an invalid review id; acknowledged without applying")
		return nil
	}
	completion, ok := moderationCompletionFromEvent(event, h.now().UTC())
	if !ok {
		log.WarnContext(ctx,
			"moderation completion has an invalid outcome or reason pairing; acknowledged without applying",
			"outcome", event.GetOutcome().String(),
			"reason_codes", len(event.GetReasonCodes()),
		)
		return nil
	}

	var applied bool
	err = h.withTransaction(ctx, func(q *reviewdb.Queries) error {
		applied, err = h.apply(ctx, log, q, eventID, reviewID, completion)
		return err
	})
	if err != nil {
		return err
	}
	if applied {
		log.InfoContext(ctx, "review moderation outcome applied",
			"status", string(completion.Status), "run_generation", completion.RunGeneration)
	}
	return nil
}

// apply runs the transaction body and reports whether the verdict was applied.
func (h *CompletionHandler) apply(
	ctx context.Context,
	log *slog.Logger,
	q *reviewdb.Queries,
	eventID, reviewID uuid.UUID,
	completion ModerationCompletion,
) (bool, error) {
	processed, err := h.store.RecordModerationProcessedEvent(ctx, q, eventID)
	if err != nil {
		return false, fmt.Errorf("record moderation processed event: %w", err)
	}
	if processed == 0 {
		log.InfoContext(ctx, "moderation completion already processed; acknowledged without applying")
		return false, nil
	}

	if err := h.store.LockReviewForModeration(ctx, q, reviewID); err != nil {
		return false, fmt.Errorf("lock review for moderation: %w", err)
	}
	// Read the review in a separate statement after the lock: a locking query
	// must not also read the row's state (LANDMINES), and a concurrent
	// soft-delete that commits while the lock is waited on must be visible.
	target, err := h.store.GetReviewForModeration(ctx, q, reviewID)
	if err != nil {
		if errors.Is(err, errReviewNotFound) {
			log.InfoContext(ctx, "moderation completion references a missing review; acknowledged without applying")
			return false, nil
		}
		return false, fmt.Errorf("read review for moderation: %w", err)
	}
	if target.Deleted {
		log.InfoContext(ctx, "moderation completion references a removed review; acknowledged without applying")
		return false, nil
	}
	if target.ModerationRequestID != completion.ModerationRequestID ||
		target.ReviewerID != completion.ReviewerID ||
		target.ContentRevision != completion.ContentRevision {
		log.WarnContext(ctx, "moderation completion does not match the saved review; acknowledged without applying",
			"saved_moderation_request_id", target.ModerationRequestID,
			"saved_reviewer_id", target.ReviewerID,
			"saved_content_revision", target.ContentRevision,
		)
		return false, nil
	}
	// A content verdict is final even if a later event claims a higher
	// generation. Only a technical error may be replaced by an explicit retry.
	if target.ModerationStatus == ModerationApproved || target.ModerationStatus == ModerationRejected {
		log.InfoContext(ctx, "moderation verdict is already final; acknowledging later completion",
			"status", string(target.ModerationStatus),
			"applied_run_generation", target.AppliedRunGeneration,
			"run_generation", completion.RunGeneration,
		)
		return false, nil
	}
	// Replays and superseded generations never replace a newer applied result.
	// A technical error can advance only through a later explicit retry.
	if completion.RunGeneration <= target.AppliedRunGeneration {
		log.InfoContext(ctx, "moderation completion is superseded; acknowledged without applying",
			"run_generation", completion.RunGeneration, "applied_run_generation", target.AppliedRunGeneration)
		return false, nil
	}

	if err := h.store.ApplyModerationOutcome(ctx, q, reviewID, completion); err != nil {
		return false, fmt.Errorf("apply moderation outcome: %w", err)
	}
	// Eligibility changed: the review now counts towards the reviewee's
	// published statistics (approval), or keeps not counting (rejection,
	// error); the recalculation converges both cases.
	if err := h.store.RecalculateStats(ctx, q, target.RevieweeID); err != nil {
		return false, fmt.Errorf("recalculate reviewee statistics: %w", err)
	}
	return true, nil
}

// moderationCompletionFromEvent validates the event's outcome and reason-code
// pairing: approval and errors carry no reason codes, rejections carry at
// least one. It returns false for anything that must never be applied,
// including the UNSPECIFIED protobuf default — a default can never approve
// content.
func moderationCompletionFromEvent(
	event *moderationeventsv1.ReviewModerationFinished,
	now time.Time,
) (ModerationCompletion, bool) {
	requestID, err := uuid.Parse(event.GetModerationRequestId())
	if err != nil || requestID == uuid.Nil {
		return ModerationCompletion{}, false
	}
	reviewerID, err := uuid.Parse(event.GetReviewerId())
	if err != nil || reviewerID == uuid.Nil {
		return ModerationCompletion{}, false
	}
	revision := event.GetContentRevision()
	if revision < 1 {
		return ModerationCompletion{}, false
	}

	completion := ModerationCompletion{
		ReviewerID:          reviewerID,
		ModerationRequestID: requestID,
		ContentRevision:     int(revision),
		RunGeneration:       event.GetRunGeneration(),
		PolicyVersion:       event.GetPolicyVersion(),
		ReasonCodes:         event.GetReasonCodes(),
	}
	if finished := event.GetFinishedAt(); finished.IsValid() {
		completion.ModeratedAt = finished.AsTime()
	} else {
		completion.ModeratedAt = now
	}
	switch event.GetOutcome() {
	case moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED:
		if len(completion.ReasonCodes) > 0 {
			return ModerationCompletion{}, false
		}
		completion.Status = ModerationApproved
	case moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED:
		if len(completion.ReasonCodes) == 0 || len(completion.ReasonCodes) > 5 {
			return ModerationCompletion{}, false
		}
		seen := make(map[string]bool)
		for _, code := range completion.ReasonCodes {
			switch code {
			case "HARASSMENT", "HATE", "THREAT", "SEXUAL_CONTENT", "SPAM":
			default:
				return ModerationCompletion{}, false
			}
			if seen[code] {
				return ModerationCompletion{}, false
			}
			seen[code] = true
		}
		completion.Status = ModerationRejected
	case moderationeventsv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR:
		if len(completion.ReasonCodes) > 0 {
			return ModerationCompletion{}, false
		}
		completion.Status = ModerationError
	default:
		return ModerationCompletion{}, false
	}
	return completion, true
}
