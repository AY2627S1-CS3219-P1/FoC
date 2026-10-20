package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

var ErrRoleEffectsUnconfigured = errs.NewFailedPreconditionError("authenticated Order role effects are not configured")
var ErrRoleEffectsRejected = errs.NewFailedPreconditionError("Order rejected the role decision; reconciliation is required")
var ErrRoleEffectsUnavailable = errs.NewUnavailableError("role decision outcome is uncertain; retry the same decision")

// RoleEffects is supplied only with an authenticated User-to-Order transport.
// Production provisioning/authorization is tracked in #29. A nil dependency
// refuses suspension/reinstatement BEFORE admitting any role or dispatch change.
type RoleEffects interface {
	ApplyRoleDecision(context.Context, models.RoleDispatch) error
}

type RoleDispatchStore interface {
	PendingRoleDispatch(context.Context, uuid.UUID) (*models.RoleDispatch, error)
	GetRoleDispatch(context.Context, uuid.UUID) (models.RoleDispatch, error)
	PrepareRoleDispatch(context.Context, *models.RoleDispatch) error
	CompleteRoleDispatch(context.Context, uuid.UUID, time.Time) error
	RejectRoleDispatch(context.Context, uuid.UUID, time.Time) error
	PendingRoleDispatches(context.Context, int) ([]models.RoleDispatch, error)
}

func (s *RoleService) applyRoleDispatch(ctx context.Context, d models.RoleDispatch) (models.User, error) {
	if s.Effects == nil {
		return models.User{}, ErrRoleEffectsUnconfigured
	}
	if d.RejectedAt != nil {
		return models.User{}, ErrRoleEffectsRejected
	}
	// A recovery scan may predate deletion. Do not send a saved role change
	// for a target that no longer exists, especially a saved reinstatement.
	if _, err := s.Users.GetByID(ctx, d.UserID); errors.Is(err, store.ErrNotFound) {
		return models.User{}, ErrUserNotFound
	} else if err != nil {
		return models.User{}, err
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := s.Effects.ApplyRoleDecision(call, d)
	cancel()
	if err != nil {
		if errors.Is(err, ErrRoleEffectsUnconfigured) {
			return models.User{}, ErrRoleEffectsUnconfigured
		}
		switch connect.CodeOf(err) {
		case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeAborted, connect.CodeAlreadyExists:
			// A definite rejection cannot be retried into a different immutable
			// decision. Retain it for reconciliation without blocking the worker.
			if saveErr := s.Dispatches.RejectRoleDispatch(ctx, d.ID, time.Now().UTC()); saveErr != nil {
				return models.User{}, fmt.Errorf("record rejected role decision: %w", saveErr)
			}
			return models.User{}, ErrRoleEffectsRejected
		default:
			return models.User{}, ErrRoleEffectsUnavailable
		}
	}
	var changed models.User
	err = s.WithTransaction(ctx, func(tx RoleStore) error {
		target, err := tx.Users.GetByIDForUpdate(ctx, d.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if tx.Dispatches == nil {
			return ErrRoleEffectsUnconfigured
		}
		stored, err := tx.Dispatches.GetRoleDispatch(ctx, d.ID)
		if err != nil {
			return err
		}
		if stored.AppliedAt != nil {
			changed = *target
			return nil
		}
		if stored.RejectedAt != nil {
			return ErrRoleEffectsRejected
		}
		if stored.UserID != d.UserID || stored.Revision != d.Revision || target.Role != d.FromRole {
			return ErrConcurrentRoleEdit
		}
		actorID := &d.AdminID
		if _, err := tx.Users.GetByID(ctx, d.AdminID); errors.Is(err, store.ErrNotFound) {
			// The immutable dispatch still records the original actor. Match the
			// existing audit FK's SET NULL behavior if that account was removed.
			actorID = nil
		} else if err != nil {
			return err
		}
		change := &models.RoleChange{
			BaseModel:  models.BaseModel{ID: d.ID, CreatedAt: d.CreatedAt},
			Userstamps: models.Userstamps{CreatedBy: actorID, UpdatedBy: actorID},
			UserID:     d.UserID, FromRole: d.FromRole, ToRole: d.ToRole, Reason: &d.Reason,
		}
		if err := tx.Admin.ChangeRole(ctx, change); err != nil {
			return err
		}
		if err := tx.Dispatches.CompleteRoleDispatch(ctx, d.ID, time.Now().UTC()); err != nil {
			return err
		}
		changed = *target
		changed.Role = d.ToRole
		changed.UpdatedAt = change.CreatedAt
		return nil
	})
	if err != nil {
		return models.User{}, fmt.Errorf("finalize role decision: %w", err)
	}
	return changed, nil
}

func (s *RoleService) RecoverRoleDispatchesOnce(ctx context.Context) error {
	if s.Effects == nil || s.Dispatches == nil {
		return ErrRoleEffectsUnconfigured
	}
	rows, err := s.Dispatches.PendingRoleDispatches(ctx, 50)
	if err != nil {
		return err
	}
	for _, d := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.applyRoleDispatch(call, d)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Role decision dispatch remains unfinished", "decision_id", d.ID, "error", err)
		}
	}
	return nil
}

func (s *RoleService) RecoverRoleDispatches(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.RecoverRoleDispatchesOnce(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Role decision recovery scan failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
