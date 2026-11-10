package moderationoutcome

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

// OutcomeRequestedTopic carries the consequences Moderation Service asks for.
const OutcomeRequestedTopic = "moderation.outcome_requested"

// Applier applies one outcome once.
type Applier interface {
	Apply(ctx context.Context, outcome Outcome) (Result, error)
}

// Consumer handles moderation.outcome_requested.
type Consumer struct {
	Outcomes Applier
}

// Handle applies one event. Events that can never be applied are logged and
// acknowledged so they cannot stall the subscription; any other failure is
// returned so the subscriber stops and the delivery is redelivered.
func (c *Consumer) Handle(ctx context.Context, meta kafka.Metadata, event *eventsv1.OutcomeRequested) error {
	log := slog.With("topic", meta.Topic, "partition", meta.Partition, "offset", meta.Offset,
		"event_id", event.GetEventId())

	outcome, err := toOutcome(event)
	if err != nil {
		log.ErrorContext(ctx, "Moderation outcome is malformed; acknowledged without applying it", "error", err)
		return nil
	}

	result, err := c.Outcomes.Apply(ctx, outcome)
	if err != nil {
		return err
	}
	if result == ResultRejected {
		log.WarnContext(ctx, "Moderation outcome was rejected; acknowledged without applying it",
			"action", outcome.Action, "report_id", outcome.ReportID, "appeal_id", outcome.AppealID)
		return nil
	}
	log.InfoContext(ctx, "Moderation outcome handled", "action", outcome.Action, "result", result)
	return nil
}

// RegisterConsumer subscribes the consumer to its topic.
func RegisterConsumer(subscriber *kafka.Subscriber, consumer *Consumer) error {
	return kafka.Register(subscriber, OutcomeRequestedTopic,
		func() *eventsv1.OutcomeRequested { return new(eventsv1.OutcomeRequested) },
		consumer.Handle)
}

func toOutcome(event *eventsv1.OutcomeRequested) (Outcome, error) {
	if event == nil {
		return Outcome{}, fmt.Errorf("%w: nil event", errInvalidOutcome)
	}

	var out Outcome
	var err error
	if out.EventID, err = requiredID(event.GetEventId(), "event_id"); err != nil {
		return Outcome{}, err
	}
	if out.ActorID, err = requiredID(event.GetActorId(), "actor_id"); err != nil {
		return Outcome{}, err
	}
	if out.ReportID, err = optionalID(event.GetReportId(), "report_id"); err != nil {
		return Outcome{}, err
	}
	if out.AppealID, err = optionalID(event.GetAppealId(), "appeal_id"); err != nil {
		return Outcome{}, err
	}
	out.Reason = event.GetReason()

	switch action := event.GetAction().(type) {
	case *eventsv1.OutcomeRequested_WarnUser:
		out.Action = ActionWarnUser
		if out.UserID, err = requiredID(action.WarnUser.GetUserId(), "warn_user.user_id"); err != nil {
			return Outcome{}, err
		}
		out.RequestID, err = requiredID(action.WarnUser.GetRequestId(), "warn_user.request_id")
	case *eventsv1.OutcomeRequested_SuspendUser:
		out.Action = ActionSuspendUser
		out.UserID, err = requiredID(action.SuspendUser.GetUserId(), "suspend_user.user_id")
	case *eventsv1.OutcomeRequested_RemoveReview:
		out.Action = ActionRemoveReview
		out.ReviewID, err = requiredID(action.RemoveReview.GetReviewId(), "remove_review.review_id")
	case *eventsv1.OutcomeRequested_RemoveWarning:
		out.Action = ActionRemoveWarning
		switch target := action.RemoveWarning.GetTarget().(type) {
		case *eventsv1.RemoveWarning_WarningId:
			out.WarningID, err = requiredID(target.WarningId, "remove_warning.warning_id")
		case *eventsv1.RemoveWarning_ByReportId:
			out.ReportID, err = requiredID(target.ByReportId, "remove_warning.by_report_id")
		default:
			err = fmt.Errorf("%w: remove_warning has no target", errInvalidOutcome)
		}
	case *eventsv1.OutcomeRequested_RestoreReview:
		out.Action = ActionRestoreReview
		out.ReviewID, err = requiredID(action.RestoreReview.GetReviewId(), "restore_review.review_id")
	case *eventsv1.OutcomeRequested_ReinstateUser:
		out.Action = ActionReinstateUser
		out.UserID, err = requiredID(action.ReinstateUser.GetUserId(), "reinstate_user.user_id")
	default:
		err = fmt.Errorf("%w: no action", errInvalidOutcome)
	}
	if err != nil {
		return Outcome{}, err
	}

	// Report consequences and reinstatement must name their report.
	needsReport := out.Action == ActionWarnUser || out.Action == ActionSuspendUser ||
		out.Action == ActionReinstateUser || (out.Action == ActionRemoveWarning && out.WarningID == uuid.Nil)
	if needsReport && out.ReportID == uuid.Nil {
		return Outcome{}, fmt.Errorf("%w: %s needs report_id", errInvalidOutcome, out.Action)
	}
	return out, nil
}

func requiredID(value, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a UUID", errInvalidOutcome, field)
	}
	return id, nil
}

func optionalID(value, field string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, nil
	}
	return requiredID(value, field)
}
