// Package reportoutcome applies a closed report's outcome (RP5.2-RP5.4) through
// the domains that own each consequence: account warnings, role changes and
// review removal. An Applier is bound to the report's closing transaction, so
// the consequence and the close commit or roll back together.
package reportoutcome

import (
	"context"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
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

var _ report.Consequences = (*Applier)(nil)

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
	roles := &service.RoleService{
		Users: store.NewUsers(a.tx),
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return operation(service.RoleStore{Users: store.NewUsers(a.tx), Admin: store.NewAdmin(a.tx)})
		},
	}

	reportID := suspension.ReportID
	_, err := roles.ChangeUserRole(
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
	queries := reviewdb.New(a.tx.Statement.ConnPool)
	reviews := review.NewService(
		review.NewPostgresRepository(queries),
		func(_ context.Context, operation func(*reviewdb.Queries) error) error { return operation(queries) },
		nil,
	)
	return reviews.RemoveReview(ctx, reviewID, reason, actorID)
}
