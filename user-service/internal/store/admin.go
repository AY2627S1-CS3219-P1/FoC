// Allowed domains, role changes and account warnings persistence.

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type Admin struct{ db *gorm.DB }

func NewAdmin(db *gorm.DB) *Admin { return &Admin{db: db} }

func (s *Admin) GetUser(ctx context.Context, id uint) (*models.User, error) {
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

func (s *Admin) ListDomains(ctx context.Context) ([]models.AllowedEmailDomain, error) {
	var ds []models.AllowedEmailDomain
	err := s.db.WithContext(ctx).Order("domain").Find(&ds).Error
	return ds, err
}

// AddDomain inserts d, mapping GORM duplicate-key and check-constraint errors
// to ErrDuplicate and ErrInvalidDomain. Other database errors are returned unchanged.
func (s *Admin) AddDomain(ctx context.Context, d *models.AllowedEmailDomain) error {
	err := s.db.WithContext(ctx).Create(d).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	if errors.Is(err, gorm.ErrCheckConstraintViolated) {
		return ErrInvalidDomain
	}
	return err
}

func (s *Admin) DeleteDomain(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&models.AllowedEmailDomain{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
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

func (s *Admin) RoleChanges(ctx context.Context, userID uint) ([]models.RoleChange, error) {
	var cs []models.RoleChange
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&cs).Error
	return cs, err
}

// CreateWarning reports true when w is inserted, or false after loading the
// existing warning with the same SourceEventID into w.
func (s *Admin) CreateWarning(ctx context.Context, w *models.AccountWarning) (bool, error) {
	db := s.db.WithContext(ctx)
	if w.SourceEventID == nil {
		return true, db.Create(w).Error
	}
	res := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_event_id"}}, DoNothing: true}).Create(w)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		return true, nil
	}
	var existing models.AccountWarning
	if err := db.Unscoped().Take(&existing, "source_event_id = ?", *w.SourceEventID).Error; err != nil {
		return false, err
	}
	*w = existing
	return false, nil
}

func (s *Admin) GetWarning(ctx context.Context, id uint) (*models.AccountWarning, error) {
	var w models.AccountWarning
	err := s.db.WithContext(ctx).Take(&w, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Admin) Warnings(ctx context.Context, userID uint) ([]models.AccountWarning, error) {
	var ws []models.AccountWarning
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&ws).Error
	return ws, err
}

func (s *Admin) RemoveWarning(ctx context.Context, id uint, now time.Time, reason *string, appealID *uuid.UUID, actor *uint) (*models.AccountWarning, error) {
	var w models.AccountWarning
	res := s.db.WithContext(ctx).Model(&w).Clauses(clause.Returning{}).
		Where("id = ? AND status = ?", id, models.WarningActive).
		Updates(map[string]any{
			"status":         models.WarningRemoved,
			"removed_at":     now,
			"removed_reason": reason,
			"appeal_id":      appealID,
			"updated_by":     actor,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &w, nil
}
