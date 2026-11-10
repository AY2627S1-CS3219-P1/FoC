// Package outbox records the consequences of closed reports and appeals in
// the closing transaction, and relays them to Kafka as
// moderation.outcome_requested for User Service to apply.
package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/outboxdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

// Topic carries every recorded outcome.
const Topic = "moderation.outcome_requested"

// writer inserts outcomes through queries bound to the closing transaction.
type writer struct {
	queries *outboxdb.Queries
	now     func() time.Time
}

// origin names the case an outcome belongs to.
type origin struct {
	actorID, reportID, appealID uuid.UUID
	reason                      string
}

// record serializes one outcome and inserts it. key orders the outcomes of
// one user or review on a Kafka partition.
func (w writer) record(ctx context.Context, key uuid.UUID, c origin, set func(*eventsv1.OutcomeRequested)) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate outcome event id: %w", err)
	}

	event := &eventsv1.OutcomeRequested{
		EventId:    eventID.String(),
		OccurredAt: timestamppb.New(w.now()),
		ActorId:    c.actorID.String(),
		Reason:     c.reason,
		ReportId:   idString(c.reportID),
		AppealId:   idString(c.appealID),
	}
	set(event)
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}

	err = w.queries.InsertOutcome(ctx, outboxdb.InsertOutcomeParams{
		EventID:    eventID,
		MessageKey: key.String(),
		Payload:    payload,
	})
	if err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	return nil
}

func idString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// Report outcomes

// ReportConsequences records a closed report's outcome.
type ReportConsequences struct{ w writer }

var _ report.Consequences = ReportConsequences{}

// ForReport binds report consequences to the closing transaction's queries.
func ForReport(queries *outboxdb.Queries) ReportConsequences {
	return ReportConsequences{w: writer{queries: queries, now: time.Now}}
}

func (r ReportConsequences) RecordWarning(ctx context.Context, warning report.Warning) error {
	c := origin{actorID: warning.ActorID, reportID: warning.ReportID, reason: warning.Reason}
	return r.w.record(ctx, warning.UserID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_WarnUser{WarnUser: &eventsv1.WarnUser{
			UserId:    warning.UserID.String(),
			RequestId: warning.RequestID.String(),
		}}
	})
}

func (r ReportConsequences) Suspend(ctx context.Context, suspension report.Suspension) error {
	c := origin{actorID: suspension.ActorID, reportID: suspension.ReportID, reason: suspension.Reason}
	return r.w.record(ctx, suspension.UserID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_SuspendUser{
			SuspendUser: &eventsv1.SuspendUser{UserId: suspension.UserID.String()},
		}
	})
}

func (r ReportConsequences) RemoveReview(ctx context.Context, removal report.ReviewRemoval) error {
	c := origin{actorID: removal.ActorID, reportID: removal.ReportID, reason: removal.Reason}
	return r.w.record(ctx, removal.ReviewID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_RemoveReview{
			RemoveReview: &eventsv1.RemoveReview{ReviewId: removal.ReviewID.String()},
		}
	})
}

// Appeal outcomes

// AppealConsequences records how to undo the decisions an appeal overturned.
type AppealConsequences struct{ w writer }

var _ appeal.Consequences = AppealConsequences{}

// ForAppeal binds appeal consequences to the closing transaction's queries.
func ForAppeal(queries *outboxdb.Queries) AppealConsequences {
	return AppealConsequences{w: writer{queries: queries, now: time.Now}}
}

// RemoveWarning keys a report's warning by the report, because only User
// Service knows the warning's ID; both events of that report share the key.
func (a AppealConsequences) RemoveWarning(ctx context.Context, removal appeal.WarningRemoval) error {
	c := origin{actorID: removal.ActorID, appealID: removal.AppealID, reason: removal.Reason}
	target := &eventsv1.RemoveWarning{Target: &eventsv1.RemoveWarning_WarningId{WarningId: removal.WarningID.String()}}
	key := removal.WarningID
	if removal.WarningID == uuid.Nil {
		c.reportID = removal.ReportID
		target = &eventsv1.RemoveWarning{
			Target: &eventsv1.RemoveWarning_ByReportId{ByReportId: removal.ReportID.String()},
		}
		key = removal.UserID
	}
	return a.w.record(ctx, key, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_RemoveWarning{RemoveWarning: target}
	})
}

func (a AppealConsequences) RestoreReview(ctx context.Context, restoration appeal.ReviewRestoration) error {
	c := origin{
		actorID:  restoration.ActorID,
		reportID: restoration.ReportID,
		appealID: restoration.AppealID,
		reason:   restoration.Reason,
	}
	return a.w.record(ctx, restoration.ReviewID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_RestoreReview{
			RestoreReview: &eventsv1.RestoreReview{ReviewId: restoration.ReviewID.String()},
		}
	})
}

func (a AppealConsequences) RemoveReview(ctx context.Context, removal appeal.ReviewRemoval) error {
	c := origin{actorID: removal.ActorID, appealID: removal.AppealID, reason: removal.Reason}
	return a.w.record(ctx, removal.ReviewID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_RemoveReview{
			RemoveReview: &eventsv1.RemoveReview{ReviewId: removal.ReviewID.String()},
		}
	})
}

func (a AppealConsequences) Reinstate(ctx context.Context, reinstatement appeal.Reinstatement) error {
	c := origin{
		actorID:  reinstatement.ActorID,
		reportID: reinstatement.ReportID,
		appealID: reinstatement.AppealID,
		reason:   reinstatement.Reason,
	}
	return a.w.record(ctx, reinstatement.UserID, c, func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_ReinstateUser{
			ReinstateUser: &eventsv1.ReinstateUser{UserId: reinstatement.UserID.String()},
		}
	})
}
