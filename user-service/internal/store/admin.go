// Allowed domains, role changes and account warnings persistence.

package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type Admin struct{ db *gorm.DB }

func NewAdmin(db *gorm.DB) *Admin { return &Admin{db: db} }

func (s *Admin) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := s.db.WithContext(ctx).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ChangeRole moves the user from c.FromRole to c.ToRole and records c in one
// transaction, or returns ErrRoleConflict if the current role differs.
// c.CreatedBy is the actor and is also stored as the user's updated_by.
func (s *Admin) ChangeRole(ctx context.Context, c *models.RoleChange) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.User{}).
			Where("id = ? AND role = ?", c.UserID, c.FromRole).
			Updates(map[string]any{"role": c.ToRole, "updated_by": c.CreatedBy})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrRoleConflict
		}
		return tx.Create(c).Error
	})
}

type BootstrapOutcome int

const (
	BootstrapCreated BootstrapOutcome = iota + 1
	BootstrapPromoted
	BootstrapAlreadyDone
)

func (o BootstrapOutcome) String() string {
	switch o {
	case BootstrapCreated:
		return "created"
	case BootstrapPromoted:
		return "promoted"
	case BootstrapAlreadyDone:
		return "already_done"
	default:
		return "unknown"
	}
}

const bootstrapReason = "admin bootstrap"

// Bootstrap makes u the first super_admin and claims the admin_bootstrap row
// in one transaction. It creates u, or promotes the live user with u.Email
// (recording a role change) and loads them into u. Once the row exists it
// returns BootstrapAlreadyDone without touching any user, so a demoted
// bootstrap admin is never restored. The table lock serialises concurrent
// callers across instances.
func (s *Admin) Bootstrap(ctx context.Context, u *models.User) (BootstrapOutcome, error) {
	var outcome BootstrapOutcome
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("LOCK TABLE admin_bootstrap IN EXCLUSIVE MODE").Error; err != nil {
			return err
		}
		var done int64
		if err := tx.Model(&models.AdminBootstrap{}).Count(&done).Error; err != nil {
			return err
		}
		if done > 0 {
			outcome = BootstrapAlreadyDone
			return nil
		}

		var existing models.User
		err := tx.Take(&existing, "email = ?", u.Email).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			u.Role = models.RoleSuperAdmin
			if err := tx.Create(u).Error; err != nil {
				return err
			}
			outcome = BootstrapCreated
		case err != nil:
			return err
		default:
			if existing.Role != models.RoleSuperAdmin {
				reason := bootstrapReason
				if err := NewAdmin(tx).ChangeRole(ctx, &models.RoleChange{UserID: existing.ID,
					FromRole: existing.Role, ToRole: models.RoleSuperAdmin, Reason: &reason}); err != nil {
					return err
				}
				existing.Role = models.RoleSuperAdmin
			}
			*u = existing
			outcome = BootstrapPromoted
		}
		return tx.Create(&models.AdminBootstrap{Singleton: true, UserID: u.ID}).Error
	})
	if err != nil {
		return 0, err
	}
	return outcome, nil
}

func (s *Admin) RoleChanges(ctx context.Context, userID uuid.UUID) ([]models.RoleChange, error) {
	var cs []models.RoleChange
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&cs).Error
	return cs, err
}
