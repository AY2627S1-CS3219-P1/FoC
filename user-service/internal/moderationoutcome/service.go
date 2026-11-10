package moderationoutcome

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

// Action is the consequence an outcome asks for.
type Action string

const (
	ActionWarnUser      Action = "warn_user"
	ActionSuspendUser   Action = "suspend_user"
	ActionRemoveReview  Action = "remove_review"
	ActionRemoveWarning Action = "remove_warning"
	ActionRestoreReview Action = "restore_review"
	ActionReinstateUser Action = "reinstate_user"
)

// Outcome is one consequence of a closed report or appeal. Only the target
// fields its action needs are set.
type Outcome struct {
	EventID  uuid.UUID
	Action   Action
	ActorID  uuid.UUID
	Reason   string
	ReportID uuid.UUID
	AppealID uuid.UUID

	UserID    uuid.UUID
	RequestID uuid.UUID
	ReviewID  uuid.UUID
	WarningID uuid.UUID
}

// Result says what happened to an outcome.
type Result string

const (
	// ResultApplied means the consequence was applied now.
	ResultApplied Result = "applied"
	// ResultRejected means the consequence can never be applied, for example
	// because the warning is already removed.
	ResultRejected Result = "rejected"
	// ResultDuplicate means the event was handled before.
	ResultDuplicate Result = "duplicate"
)

// processedEvent records each handled event in moderation_outcome_events.
type processedEvent struct {
	EventID uuid.UUID `gorm:"primaryKey"`
	Outcome string
}

func (processedEvent) TableName() string { return "moderation_outcome_events" }

// Service applies outcomes once each.
type Service struct {
	DB *gorm.DB
}

// Apply records the event and applies its consequence in one transaction. A
// rejection is recorded so redelivery does not retry it; any other failure
// rolls everything back and is returned for redelivery.
func (s *Service) Apply(ctx context.Context, outcome Outcome) (Result, error) {
	result := ResultApplied
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claimed := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&processedEvent{EventID: outcome.EventID, Outcome: string(ResultApplied)})
		if claimed.Error != nil {
			return fmt.Errorf("record outcome event: %w", claimed.Error)
		}
		if claimed.RowsAffected == 0 {
			result = ResultDuplicate
			return nil
		}

		// The consequence runs in a savepoint, so a rejection undoes only its
		// partial writes and the event is still recorded as handled.
		applyErr := tx.Transaction(func(inner *gorm.DB) error {
			return apply(ctx, &applier{tx: inner}, outcome)
		})
		if applyErr == nil {
			return nil
		}
		if !isRejection(applyErr) {
			return applyErr
		}

		result = ResultRejected
		return tx.Model(&processedEvent{}).
			Where("event_id = ?", outcome.EventID).
			Update("outcome", string(ResultRejected)).Error
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

func apply(ctx context.Context, a *applier, o Outcome) error {
	switch o.Action {
	case ActionWarnUser:
		return a.warnUser(ctx, o, o.UserID, o.RequestID)
	case ActionSuspendUser:
		return a.suspendUser(ctx, o, o.UserID)
	case ActionRemoveReview:
		return a.removeReview(ctx, o, o.ReviewID)
	case ActionRemoveWarning:
		return a.removeWarning(ctx, o, o.WarningID, o.ReportID)
	case ActionRestoreReview:
		return a.restoreReview(ctx, o, o.ReviewID)
	case ActionReinstateUser:
		return a.reinstateUser(ctx, o, o.UserID)
	default:
		return fmt.Errorf("%w: unknown action %q", errInvalidOutcome, o.Action)
	}
}

var errInvalidOutcome = errors.New("invalid moderation outcome")

// isRejection reports a failure that retrying can never fix: the domain
// refused the change, or the target no longer allows it.
func isRejection(err error) bool {
	switch {
	case errors.Is(err, errInvalidOutcome),
		errors.Is(err, warningdomain.ErrNotFound),
		errors.Is(err, service.ErrUnauthenticated),
		errors.Is(err, service.ErrInvalidRoleChange),
		errors.Is(err, service.ErrPermissionDenied),
		errors.Is(err, service.ErrUserNotFound),
		errors.Is(err, service.ErrRoleUnchanged):
		return true
	}

	var external api.ExternalError
	if errors.As(err, &external) {
		code := external.Code()
		return code >= http.StatusBadRequest && code < http.StatusInternalServerError
	}
	return false
}
