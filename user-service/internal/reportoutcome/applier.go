// Package reportoutcome applies a closed report's outcome (RP5.2-RP5.4), and
// undoes the decisions an appeal overturns (RP12), through the domains that own
// each consequence: account warnings, role changes and review removal. An
// Applier is bound to the closing transaction, so the consequence and the close
// commit or roll back together.
package reportoutcome

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Applier runs every consequence on one GORM transaction.
type Applier struct {
	tx *gorm.DB
}

var (
	_ report.Consequences = (*Applier)(nil)
	_ appeal.Consequences = (*Applier)(nil)
)

// New binds an Applier to tx, which must be a GORM transaction.
func New(tx *gorm.DB) *Applier { return &Applier{tx: tx} }

// WithTransaction runs each report operation in one GORM transaction. The
// sqlc report queries use the transaction's connection, so report rows and
// consequences commit together.
func WithTransaction(db *gorm.DB) report.WithTransaction {
	return func(ctx context.Context, operation func(report.Tx) error) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return operation(report.Tx{
				Queries:      reportdb.New(tx.Statement.ConnPool),
				Consequences: New(tx),
				Evidence:     evidence.NewAttacher(tx.Statement.ConnPool),
			})
		})
	}
}

// WithAppealTransaction runs each appeal operation in one GORM transaction, so
// an appeal's close and the outcomes it overturns commit together.
func WithAppealTransaction(db *gorm.DB) appeal.WithTransaction {
	return func(ctx context.Context, operation func(appeal.Tx) error) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return operation(appeal.Tx{
				Queries:      appealdb.New(tx.Statement.ConnPool),
				Consequences: New(tx),
			})
		})
	}
}

// RecordWarning records an account warning that references the report.
func (a *Applier) RecordWarning(ctx context.Context, warning report.Warning) error {
	reportID, actorID := warning.ReportID, warning.ActorID
	warnings := warningdomain.NewPostgresRepository(warningdb.New(a.tx.Statement.ConnPool))
	_, err := warnings.Create(ctx, warningdomain.Warning{
		UserID:    warning.UserID,
		RequestID: warning.RequestID,
		ReportID:  &reportID,
		Reason:    warning.Reason,
		Status:    warningdomain.StatusActive,
		CreatedBy: &actorID,
		UpdatedBy: &actorID,
	})
	if err != nil {
		return fmt.Errorf("record report warning: %w", err)
	}
	return nil
}

// Suspend applies the role service's rules, so an administrator cannot suspend
// another administrator through a report.
func (a *Applier) Suspend(ctx context.Context, suspension report.Suspension) error {
	reportID := suspension.ReportID
	_, err := a.roles().ChangeUserRole(
		ctx,
		suspension.ActorID,
		suspension.UserID,
		models.RoleSuspended,
		suspension.Reason,
		&reportID,
	)
	return err
}

// RemoveReview soft-deletes the review with its removal audit and recomputes
// the reviewee's rating, on the closing transaction's connection.
func (a *Applier) RemoveReview(ctx context.Context, actorID, reviewID uuid.UUID, reason string) error {
	return a.reviews().RemoveReview(ctx, reviewID, reason, actorID)
}

// Appeal outcomes

// RemoveWarning marks an active warning removed and links the appeal that
// overturned it (RP12.1).
func (a *Applier) RemoveWarning(ctx context.Context, removal appeal.WarningRemoval) error {
	warnings := warningdomain.NewPostgresRepository(warningdb.New(a.tx.Statement.ConnPool))
	reason, appealID, actorID := removal.Reason, removal.AppealID, removal.ActorID
	_, err := warnings.Remove(ctx, removal.WarningID, time.Now().UTC(), &reason, &appealID, &actorID)
	if errors.Is(err, warningdomain.ErrNotFound) {
		return errs.WrapFailedPreconditionError(err, "the appealed warning is no longer active")
	}
	if err != nil {
		return fmt.Errorf("remove appealed warning: %w", err)
	}
	return nil
}

// RestoreReview restores a removed review and recomputes the reviewee's
// rating (RP12.3).
func (a *Applier) RestoreReview(ctx context.Context, restoration appeal.ReviewRestoration) error {
	return a.reviews().RestoreReview(
		ctx,
		restoration.ReviewID,
		restoration.Reason,
		restoration.AppealID,
		restoration.ActorID,
	)
}

// Reinstate lifts a suspension a report imposed, returning the user to the
// role they had before it. A user who is no longer suspended keeps their
// current role, and the appeal records the disposition only.
func (a *Applier) Reinstate(ctx context.Context, reinstatement appeal.Reinstatement) error {
	user, err := store.NewUsers(a.tx).GetByID(ctx, reinstatement.UserID)
	if err != nil {
		return fmt.Errorf("read suspended user: %w", err)
	}
	if user.Role != models.RoleSuspended {
		return nil
	}

	previous := models.RoleUser
	changes, err := store.NewAdmin(a.tx).RoleChanges(ctx, reinstatement.UserID)
	if err != nil {
		return fmt.Errorf("read role changes: %w", err)
	}
	for _, change := range changes {
		if change.ReportID != nil && *change.ReportID == reinstatement.ReportID &&
			change.ToRole == models.RoleSuspended {
			previous = change.FromRole
			break
		}
	}

	reportID := reinstatement.ReportID
	_, err = a.roles().ChangeUserRole(
		ctx,
		reinstatement.ActorID,
		reinstatement.UserID,
		previous,
		reinstatement.Reason,
		&reportID,
	)
	return err
}

func (a *Applier) roles() *service.RoleService {
	return &service.RoleService{
		Users: store.NewUsers(a.tx),
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return operation(service.RoleStore{Users: store.NewUsers(a.tx), Admin: store.NewAdmin(a.tx)})
		},
	}
}

// reviews runs the review service on the closing transaction's connection.
func (a *Applier) reviews() *review.Service {
	queries := reviewdb.New(a.tx.Statement.ConnPool)
	return review.NewService(
		review.NewPostgresRepository(queries),
		func(_ context.Context, operation func(*reviewdb.Queries) error) error { return operation(queries) },
		nil,
	)
}
