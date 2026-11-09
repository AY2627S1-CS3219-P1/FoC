package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

// OutstandingWork is an authoritative, read-only snapshot from a neighbouring
// service. Reasons contain safe, adapter-owned descriptions, never remote errors.
type OutstandingWork struct {
	HasOutstandingWork bool
	Reasons            []string
}

type OutstandingWorkReader interface {
	GetUserOutstandingWork(context.Context, uuid.UUID) (OutstandingWork, error)
}

type AccountDeleter interface {
	HardDeleteAccount(context.Context, *models.User, models.UserDeletedOutbox) error
}

type DeletionStore struct {
	Users    RoleUsers
	Accounts AccountDeleter
}

type DeletionTransaction func(context.Context, func(DeletionStore) error) error

// DeletionService extends account administration. There is no accepted/deleting
// state: a failed check leaves the account unchanged and the admin can retry.
type DeletionService struct {
	Users           RoleUsers
	Order           OutstandingWorkReader
	Credit          OutstandingWorkReader
	WithTransaction DeletionTransaction
	Now             func() time.Time
}

func (s *DeletionService) DeleteUser(ctx context.Context, actorID, targetID uuid.UUID) error {
	ctx, cancelRequest := context.WithTimeout(ctx, 15*time.Second)
	defer cancelRequest()

	if actorID == uuid.Nil {
		return ErrUnauthenticated
	}
	if targetID == uuid.Nil {
		return errs.NewBadRequestError("invalid user ID")
	}
	actor, err := (&RoleService{Users: s.Users}).currentAdmin(ctx, actorID)
	if err != nil {
		return err
	}
	target, err := s.Users.GetByID(ctx, targetID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("find deletion target: %w", err)
	}
	if err := authorizeDeletion(actor, target); err != nil {
		return err
	}

	// Checks run in this fixed order and outside the local transaction. These
	// are point-in-time preconditions, not distributed admission locks.
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for _, check := range []struct {
		name   string
		reader OutstandingWorkReader
	}{{"Order", s.Order}, {"Credit", s.Credit}} {
		if check.reader == nil {
			return errs.NewUnavailableError(
				"Could not verify outstanding work with " + check.name + " Service; try again.",
			)
		}
		call, cancelCall := context.WithTimeout(checkCtx, 5*time.Second)
		work, err := check.reader.GetUserOutstandingWork(call, targetID)
		if err == nil {
			err = call.Err()
		}
		cancelCall()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errs.WrapUnavailableError(err,
				"Could not verify outstanding work with "+check.name+" Service; try again.")
		}
		if work.HasOutstandingWork || len(work.Reasons) != 0 {
			reason := "Account has outstanding work"
			if len(work.Reasons) != 0 {
				reason = work.Reasons[0]
			}
			return errs.NewFailedPreconditionError(check.name + " Service: " + reason + ".")
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	fact := models.UserDeletedOutbox{
		EventID: uuid.New(), UserID: targetID, DeletionRequestID: uuid.New(),
		DeletedAt: now().UTC(), PayloadVersion: 1,
	}
	err = s.WithTransaction(ctx, func(tx DeletionStore) error {
		// Re-read current roles under locks; JWT claims and the earlier lookup
		// cannot authorize a delete after a concurrent demotion/promotion.
		actor, err := tx.Users.GetByIDForUpdate(ctx, actorID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrUnauthenticated
		}
		if err != nil {
			return err
		}
		target, err := tx.Users.GetByIDForUpdate(ctx, targetID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if err := authorizeDeletion(actor, target); err != nil {
			return err
		}
		if err := tx.Accounts.HardDeleteAccount(ctx, target, fact); err != nil {
			if errors.Is(err, store.ErrInUse) {
				return errs.NewFailedPreconditionError("This protected account cannot be deleted.")
			}
			if errors.Is(err, store.ErrProfileImageCleanup) {
				return errs.NewFailedPreconditionError(
					"Profile image cleanup must be configured before deleting this account.",
				)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete user account: %w", err)
	}
	return nil
}

func authorizeDeletion(actor, target *models.User) error {
	if !actor.Role.IsAdmin() || actor.ID == target.ID || target.Role == models.RoleSuperAdmin ||
		(actor.Role == models.RoleAdmin && target.Role == models.RoleAdmin) {
		return ErrPermissionDenied
	}
	return nil
}
