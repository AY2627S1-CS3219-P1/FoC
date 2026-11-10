// Package moderationoutcome applies the consequences Moderation Service asks
// for when it closes a report or appeal: account warnings, suspensions and
// their reinstatement, and review removal and restoration. Each consequence
// goes through the domain that owns it, inside one transaction that also
// records the event, so a redelivered event is applied at most once.
package moderationoutcome

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/orders"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

// applier runs every consequence on one GORM transaction. GORM stores use the
// transaction directly; sqlc queries use its connection.
type applier struct {
	tx *gorm.DB
}

// Report outcomes

func (a *applier) warnUser(ctx context.Context, o Outcome, userID, requestID uuid.UUID) error {
	reportID, actorID := o.ReportID, o.ActorID
	_, err := a.warnings().Create(ctx, warningdomain.Warning{
		UserID:    userID,
		RequestID: requestID,
		ReportID:  &reportID,
		Reason:    o.Reason,
		Status:    warningdomain.StatusActive,
		CreatedBy: &actorID,
		UpdatedBy: &actorID,
	})
	if err != nil {
		return fmt.Errorf("record report warning: %w", err)
	}
	return nil
}

// suspendUser applies the role service's rules, so a report cannot suspend
// an administrator.
func (a *applier) suspendUser(ctx context.Context, o Outcome, userID uuid.UUID) error {
	reportID := o.ReportID
	_, err := a.roles().ChangeUserRole(ctx, o.ActorID, userID, models.RoleSuspended, o.Reason, &reportID)
	return err
}

// removeReview links the removal to the report whose outcome it is, if any.
func (a *applier) removeReview(ctx context.Context, o Outcome, reviewID uuid.UUID) error {
	return a.reviews().RemoveReviewForReport(ctx, reviewID, o.Reason, o.ReportID, o.ActorID)
}

// Appeal outcomes

// removeWarning marks a warning removed and links the appeal that overturned
// it. A warning a report issued is found through that report.
func (a *applier) removeWarning(ctx context.Context, o Outcome, warningID, byReportID uuid.UUID) error {
	warnings := a.warnings()
	if warningID == uuid.Nil {
		found, err := warnings.ActiveForReport(ctx, byReportID)
		if err != nil {
			return fmt.Errorf("find the report's warning: %w", err)
		}
		warningID = found
	}

	reason, appealID, actorID := o.Reason, o.AppealID, o.ActorID
	_, err := warnings.Remove(ctx, warningID, time.Now().UTC(), &reason, &appealID, &actorID)
	if err != nil {
		return fmt.Errorf("remove appealed warning: %w", err)
	}
	return nil
}

func (a *applier) restoreReview(ctx context.Context, o Outcome, reviewID uuid.UUID) error {
	return a.reviews().RestoreReview(ctx, reviewID, o.Reason, o.AppealID, o.ActorID)
}

// reinstateUser lifts a suspension the report imposed, returning the user to
// the role they had before it. A user who is no longer suspended keeps their
// current role.
func (a *applier) reinstateUser(ctx context.Context, o Outcome, userID uuid.UUID) error {
	user, err := store.NewUsers(a.tx).GetByID(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return service.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("read suspended user: %w", err)
	}
	if user.Role != models.RoleSuspended {
		return nil
	}

	previous := models.RoleUser
	changes, err := store.NewAdmin(a.tx).RoleChanges(ctx, userID)
	if err != nil {
		return fmt.Errorf("read role changes: %w", err)
	}
	for _, change := range changes {
		if change.ReportID != nil && *change.ReportID == o.ReportID && change.ToRole == models.RoleSuspended {
			previous = change.FromRole
			break
		}
	}

	reportID := o.ReportID
	_, err = a.roles().ChangeUserRole(ctx, o.ActorID, userID, previous, o.Reason, &reportID)
	return err
}

// Domain services bound to the transaction

func (a *applier) warnings() *warningdomain.PostgresRepository {
	return warningdomain.NewPostgresRepository(warningdb.New(a.tx.Statement.ConnPool))
}

func (a *applier) roles() *service.RoleService {
	return &service.RoleService{
		Users: store.NewUsers(a.tx),
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return operation(service.RoleStore{Users: store.NewUsers(a.tx), Admin: store.NewAdmin(a.tx)})
		},
	}
}

func (a *applier) reviews() *review.Service {
	queries := reviewdb.New(a.tx.Statement.ConnPool)
	return review.NewService(
		review.NewPostgresRepository(queries),
		func(_ context.Context, operation func(*reviewdb.Queries) error) error { return operation(queries) },
		nil,
		// Outcomes only remove and restore reviews, which need no order lookup.
		orders.UnavailableGateway{},
	)
}
