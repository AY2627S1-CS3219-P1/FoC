// User profile and favourite supplier persistence.

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

type ListParams struct {
	Limit  int
	Offset int
	Role   *models.RoleName
}

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

func (p ListParams) Normalize() ListParams {
	if p.Limit <= 0 {
		p.Limit = DefaultPageSize
	}
	if p.Limit > MaxPageSize {
		p.Limit = MaxPageSize
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

// UserUpdate is a partial profile update: nil leaves a field unchanged, and ""
// clears TelegramHandle or PhoneNumber. UpdatedBy is the acting user (nil = system).
type UserUpdate struct {
	DisplayName    *string
	Description    *string
	TelegramHandle *string
	PhoneNumber    *string
	UpdatedBy      *uint
}

type Users struct{ db *gorm.DB }

func NewUsers(db *gorm.DB) *Users { return &Users{db: db} }

// GetByEmail returns the user or ErrNotFound if absent.
func (s *Users) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	err := s.db.WithContext(ctx).Take(&user, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Users) Create(ctx context.Context, user *models.User) error {
	err := s.db.WithContext(ctx).Create(user).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	return err
}

func (s *Users) GetByID(ctx context.Context, id uint) (*models.User, error) {
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

// GetByIDForUpdate locks a user row until the surrounding transaction ends.
func (s *Users) GetByIDForUpdate(ctx context.Context, id uint) (*models.User, error) {
	var u models.User
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateActiveProfile replaces the editable fields only while the account is
// active. The role predicate closes the race with a concurrent suspension.
func (s *Users) UpdateActiveProfile(ctx context.Context, user *models.User) error {
	fields := map[string]any{
		"display_name":    user.DisplayName,
		"description":     user.Description,
		"telegram_handle": nullString(user.TelegramHandle),
		"phone_number":    nullString(user.PhoneNumber),
		"updated_by":      user.UpdatedBy,
	}
	res := s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ? AND role <> ?", user.ID, models.RoleSuspended).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var current models.User
		if err := s.db.WithContext(ctx).Take(&current, user.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		return ErrRoleConflict
	}
	return nil
}

func nullString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func (s *Users) List(ctx context.Context, p ListParams) ([]models.User, int64, error) {
	p = p.Normalize()
	var (
		users []models.User
		total int64
	)
	q := s.db.WithContext(ctx).Model(&models.User{})
	if p.Role != nil {
		q = q.Where("role = ?", *p.Role)
	}
	q = q.Session(&gorm.Session{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC, id").Limit(p.Limit).Offset(p.Offset).Find(&users).Error
	return users, total, err
}

func (s *Users) Update(ctx context.Context, id uint, in UserUpdate) error {
	fields := map[string]any{"updated_by": in.UpdatedBy}
	if in.DisplayName != nil {
		fields["display_name"] = *in.DisplayName
	}
	if in.Description != nil {
		fields["description"] = *in.Description
	}
	if in.TelegramHandle != nil {
		fields["telegram_handle"] = nullIfEmpty(*in.TelegramHandle)
	}
	if in.PhoneNumber != nil {
		fields["phone_number"] = nullIfEmpty(*in.PhoneNumber)
	}
	res := s.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// Delete soft-deletes the user and their sessions and tokens, and removes
// their favourites. Warnings and role changes are kept as history.
func (s *Users) Delete(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bootstraps int64
		if err := tx.Model(&models.AdminBootstrap{}).Where("user_id = ?", id).Count(&bootstraps).Error; err != nil {
			return err
		}
		if bootstraps > 0 {
			return ErrInUse
		}
		res := tx.Delete(&models.User{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Where("user_id = ?", id).Delete(&models.Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&models.AuthToken{}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", id).Delete(&models.FavouriteSupplier{}).Error
	})
}

func (s *Users) ListFavourites(ctx context.Context, userID uint) ([]models.FavouriteSupplier, error) {
	var favs []models.FavouriteSupplier
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&favs).Error
	return favs, err
}

func (s *Users) AddFavourite(ctx context.Context, userID uint, supplierID uuid.UUID, now time.Time) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.FavouriteSupplier{UserID: userID, SupplierID: supplierID, CreatedAt: now}).Error
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return ErrNotFound
	}
	return err
}

func (s *Users) RemoveFavourite(ctx context.Context, userID uint, supplierID uuid.UUID) error {
	return s.db.WithContext(ctx).
		Delete(&models.FavouriteSupplier{}, "user_id = ? AND supplier_id = ?", userID, supplierID).Error
}
