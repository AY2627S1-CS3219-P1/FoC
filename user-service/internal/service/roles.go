package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type RoleUsers interface {
	GetByID(context.Context, uuid.UUID) (*models.User, error)
	GetByEmail(context.Context, string) (*models.User, error)
	GetByIDForUpdate(context.Context, uuid.UUID) (*models.User, error)
}

type RoleChanges interface {
	ChangeRole(context.Context, *models.RoleChange) error
}

type RoleStore struct {
	Users RoleUsers
	Admin RoleChanges
}

type RoleTransaction func(context.Context, func(RoleStore) error) error

type RoleService struct {
	Users           RoleUsers
	WithTransaction RoleTransaction
}

func (s *RoleService) GetUserByEmail(ctx context.Context, actorID uuid.UUID, email string) (models.User, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return models.User{}, err
	}
	if _, err := s.currentAdmin(ctx, actorID); err != nil {
		return models.User{}, err
	}
	target, err := s.Users.GetByEmail(ctx, normalized)
	if errors.Is(err, store.ErrNotFound) {
		return models.User{}, ErrUserNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("find user by email: %w", err)
	}
	return *target, nil
}

func (s *RoleService) ChangeUserRole(ctx context.Context, actorID, targetID uuid.UUID, to models.RoleName, reason string) (models.User, error) {
	if actorID == uuid.Nil {
		return models.User{}, ErrUnauthenticated
	}
	if targetID == uuid.Nil || (to != models.RoleAdmin && to != models.RoleUser && to != models.RoleSuspended) {
		return models.User{}, ErrInvalidRoleChange
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 2000 {
		return models.User{}, ErrInvalidRoleChange
	}
	var changed models.User
	err := s.WithTransaction(ctx, func(tx RoleStore) error {
		actor, err := tx.Users.GetByIDForUpdate(ctx, actorID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrUnauthenticated
		}
		if err != nil {
			return err
		}
		if !actor.Role.IsAdmin() {
			return ErrPermissionDenied
		}
		if actorID == targetID {
			return ErrPermissionDenied
		}
		target, err := tx.Users.GetByID(ctx, targetID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if target.Role == models.RoleSuperAdmin ||
			(actor.Role == models.RoleAdmin && (target.Role == models.RoleAdmin || to == models.RoleAdmin)) {
			return ErrPermissionDenied
		}
		if target.Role == to {
			return ErrRoleUnchanged
		}
		if (target.Role == models.RoleSuspended || to == models.RoleSuspended) && reason == "" {
			return ErrInvalidRoleChange
		}
		change := &models.RoleChange{UserID: targetID, FromRole: target.Role, ToRole: to, Userstamps: models.Userstamps{CreatedBy: &actorID, UpdatedBy: &actorID}}
		if reason != "" {
			change.Reason = &reason
		}
		if err := tx.Admin.ChangeRole(ctx, change); err != nil {
			if errors.Is(err, store.ErrRoleConflict) {
				return ErrConcurrentRoleEdit
			}
			return err
		}
		changed = *target
		changed.Role = to
		changed.UpdatedAt = change.CreatedAt
		return nil
	})
	if err != nil {
		return models.User{}, fmt.Errorf("change user role: %w", err)
	}
	return changed, nil
}

func (s *RoleService) currentAdmin(ctx context.Context, actorID uuid.UUID) (*models.User, error) {
	if actorID == uuid.Nil {
		return nil, ErrUnauthenticated
	}
	actor, err := s.Users.GetByID(ctx, actorID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("get actor: %w", err)
	}
	if !actor.Role.IsAdmin() {
		return nil, ErrPermissionDenied
	}
	return actor, nil
}
