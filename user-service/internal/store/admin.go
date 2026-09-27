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

// Admin persists allowed domains, role changes and account warnings.
type Admin struct{ db *gorm.DB }

// NewAdmin uses db for admin and moderation persistence.
func NewAdmin(db *gorm.DB) *Admin { return &Admin{db: db} }

// GetUser returns the user or ErrNotFound if absent. Other database errors
// are returned unchanged.
func (s *Admin) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := s.db.WithContext(ctx).Take(&u, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListDomains returns allowed domains in domain order and any database error.
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

// DeleteDomain deletes the allowed domain or returns ErrNotFound if absent.
// Other database errors are returned unchanged.
func (s *Admin) DeleteDomain(ctx context.Context, id uuid.UUID) error {
	res := s.db.WithContext(ctx).Delete(&models.AllowedEmailDomain{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangeRole atomically updates the user from c.FromRole to c.ToRole and records c.
// A missing user or mismatched current role returns ErrRoleConflict; database errors
// are propagated. A zero c.CreatedAt is filled with the current UTC time, which
// also becomes the user's updated_at. Changes to c can remain after a rollback.
func (s *Admin) ChangeRole(ctx context.Context, c *models.RoleChange) error {
	// Set before the update so users.updated_at never gets the zero time.
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.User{}).
			Where("id = ? AND role = ?", c.UserID, c.FromRole).
			Updates(map[string]any{"role": c.ToRole, "updated_at": c.CreatedAt})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrRoleConflict
		}
		return tx.Create(c).Error
	})
}

// RoleChanges returns the user's role history, newest first, and any database error.
func (s *Admin) RoleChanges(ctx context.Context, userID uuid.UUID) ([]models.RoleChange, error) {
	var cs []models.RoleChange
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&cs).Error
	return cs, err
}

// CreateWarning inserts w or loads the existing warning into w on a SourceEventID
// conflict. With a nil error, the result is true for an insert and false for a replay.
// A nil SourceEventID disables deduplication. Insert and lookup errors are propagated;
// the boolean alone does not indicate success.
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
	return false, db.Take(w, "source_event_id = ?", *w.SourceEventID).Error
}

// GetWarning returns a warning of either status or ErrNotFound if absent.
// Other database errors are returned unchanged.
func (s *Admin) GetWarning(ctx context.Context, id uuid.UUID) (*models.AccountWarning, error) {
	var w models.AccountWarning
	err := s.db.WithContext(ctx).Take(&w, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// Warnings returns the user's active and removed warnings, newest first,
// and any database error.
func (s *Admin) Warnings(ctx context.Context, userID uuid.UUID) ([]models.AccountWarning, error) {
	var ws []models.AccountWarning
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&ws).Error
	return ws, err
}

// RemoveWarning marks an active warning removed at now, stores the optional
// reason and appeal ID, and returns the updated warning. A missing or already
// removed warning returns ErrNotFound; database errors are propagated.
func (s *Admin) RemoveWarning(ctx context.Context, id uuid.UUID, now time.Time, reason *string, appealID *uuid.UUID) (*models.AccountWarning, error) {
	var w models.AccountWarning
	err := s.db.WithContext(ctx).Raw(`
		UPDATE account_warnings
		SET status = 'removed', removed_at = ?, removed_reason = ?, appeal_id = ?
		WHERE id = ? AND status = 'active'
		RETURNING *`, now, reason, appealID, id).Scan(&w).Error
	if err != nil {
		return nil, err
	}
	if w.ID == uuid.Nil {
		return nil, ErrNotFound
	}
	return &w, nil
}
