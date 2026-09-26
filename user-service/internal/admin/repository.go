package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

var (
	errDuplicate     = errors.New("duplicate")
	errInvalidDomain = errors.New("invalid domain")
	errRoleConflict  = errors.New("role changed concurrently")
	errNotFound      = errors.New("not found")
)

type Repository interface {
	GetUser(ctx context.Context, id uuid.UUID) (*models.User, error)

	ListDomains(ctx context.Context) ([]models.AllowedEmailDomain, error)
	AddDomain(ctx context.Context, d *models.AllowedEmailDomain) error // errDuplicate, errInvalidDomain
	DeleteDomain(ctx context.Context, id uuid.UUID) error              // errNotFound

	// ChangeRole updates users.role only if it is still `from` and records
	// the change, in one tx. errRoleConflict if the role moved underneath us.
	ChangeRole(ctx context.Context, c *models.RoleChange) error
	RoleChanges(ctx context.Context, userID uuid.UUID) ([]models.RoleChange, error)

	// CreateWarning is idempotent on SourceEventID: a replay returns the
	// existing row with created=false.
	CreateWarning(ctx context.Context, w *models.AccountWarning) (created bool, err error)
	GetWarning(ctx context.Context, id uuid.UUID) (*models.AccountWarning, error) // errNotFound
	Warnings(ctx context.Context, userID uuid.UUID) ([]models.AccountWarning, error)
	// RemoveWarning marks an active warning removed. errNotFound if it
	// doesn't exist or is already removed.
	RemoveWarning(ctx context.Context, id uuid.UUID, now time.Time, reason *string, appealID *uuid.UUID) (*models.AccountWarning, error)
}

type gormRepository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

func (r *gormRepository) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := r.db.WithContext(ctx).Take(&u, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *gormRepository) ListDomains(ctx context.Context) ([]models.AllowedEmailDomain, error) {
	var ds []models.AllowedEmailDomain
	err := r.db.WithContext(ctx).Order("domain").Find(&ds).Error
	return ds, err
}

func (r *gormRepository) AddDomain(ctx context.Context, d *models.AllowedEmailDomain) error {
	err := r.db.WithContext(ctx).Create(d).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return errDuplicate
	}
	if errors.Is(err, gorm.ErrCheckConstraintViolated) {
		return errInvalidDomain
	}
	return err
}

func (r *gormRepository) DeleteDomain(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&models.AllowedEmailDomain{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errNotFound
	}
	return nil
}

func (r *gormRepository) ChangeRole(ctx context.Context, c *models.RoleChange) error {
	// Set before the update so users.updated_at never gets the zero time.
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.User{}).
			Where("id = ? AND role = ?", c.UserID, c.FromRole).
			Updates(map[string]any{"role": c.ToRole, "updated_at": c.CreatedAt})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errRoleConflict
		}
		return tx.Create(c).Error
	})
}

func (r *gormRepository) RoleChanges(ctx context.Context, userID uuid.UUID) ([]models.RoleChange, error) {
	var cs []models.RoleChange
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&cs).Error
	return cs, err
}

func (r *gormRepository) CreateWarning(ctx context.Context, w *models.AccountWarning) (bool, error) {
	db := r.db.WithContext(ctx)
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

func (r *gormRepository) GetWarning(ctx context.Context, id uuid.UUID) (*models.AccountWarning, error) {
	var w models.AccountWarning
	err := r.db.WithContext(ctx).Take(&w, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *gormRepository) Warnings(ctx context.Context, userID uuid.UUID) ([]models.AccountWarning, error) {
	var ws []models.AccountWarning
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&ws).Error
	return ws, err
}

func (r *gormRepository) RemoveWarning(ctx context.Context, id uuid.UUID, now time.Time, reason *string, appealID *uuid.UUID) (*models.AccountWarning, error) {
	var w models.AccountWarning
	err := r.db.WithContext(ctx).Raw(`
		UPDATE account_warnings
		SET status = 'removed', removed_at = ?, removed_reason = ?, appeal_id = ?
		WHERE id = ? AND status = 'active'
		RETURNING *`, now, reason, appealID, id).Scan(&w).Error
	if err != nil {
		return nil, err
	}
	if w.ID == uuid.Nil {
		return nil, errNotFound
	}
	return &w, nil
}
