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

// ListParams selects a page of users, optionally filtered by role.
type ListParams struct {
	Limit  int
	Offset int
	Role   *models.RoleName // optional filter
}

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Normalize returns a copy with nonpositive limits set to DefaultPageSize,
// limits above MaxPageSize capped, and negative offsets set to zero.
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

// Users persists user profiles and favourites.
type Users struct{ db *gorm.DB }

// NewUsers uses db for user profiles and favourites.
func NewUsers(db *gorm.DB) *Users { return &Users{db: db} }

// GetByID returns the user or ErrNotFound if absent. Other database errors
// are returned unchanged.
func (s *Users) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
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

// List returns a page of users ordered by creation time descending, then ID,
// and the total count before pagination, optionally filtered by role. It does
// not normalize p. The count and page are separate queries; errors from either
// are propagated.
func (s *Users) List(ctx context.Context, p ListParams) ([]models.User, int64, error) {
	var (
		users []models.User
		total int64
	)
	q := s.db.WithContext(ctx).Model(&models.User{})
	if p.Role != nil {
		q = q.Where("role = ?", *p.Role)
	}
	// New session so Count and Find don't share one mutable statement.
	q = q.Session(&gorm.Session{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC, id").Limit(p.Limit).Offset(p.Offset).Find(&users).Error
	return users, total, err
}

// Update writes the mutable profile columns only; Select makes GORM persist
// NULLs (e.g. clearing a phone number).
// It returns ErrNotFound if no row is updated, or propagates the database error.
func (s *Users) Update(ctx context.Context, u *models.User) error {
	res := s.db.WithContext(ctx).Model(u).
		Select("display_name", "description", "telegram_handle", "phone_number", "updated_at").
		Updates(u)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the user and dependent rows configured to cascade. It returns
// ErrInUse for a GORM foreign-key violation, ErrNotFound if no user was deleted,
// or the unchanged database error otherwise.
func (s *Users) Delete(ctx context.Context, id uuid.UUID) error {
	res := s.db.WithContext(ctx).Delete(&models.User{}, "id = ?", id)
	if errors.Is(res.Error, gorm.ErrForeignKeyViolated) {
		return ErrInUse
	}
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListFavourites returns the user's favourites, newest first, and any database error.
func (s *Users) ListFavourites(ctx context.Context, userID uuid.UUID) ([]models.FavouriteSupplier, error) {
	var favs []models.FavouriteSupplier
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&favs).Error
	return favs, err
}

// AddFavourite records the supplier as a favourite at now. Existing favourites
// are unchanged. Supplier existence is not checked. A GORM foreign-key violation
// returns ErrNotFound; other database errors are propagated.
func (s *Users) AddFavourite(ctx context.Context, userID, supplierID uuid.UUID, now time.Time) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.FavouriteSupplier{UserID: userID, SupplierID: supplierID, CreatedAt: now}).Error
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return ErrNotFound
	}
	return err
}

// RemoveFavourite deletes the pair, succeeding if it is already absent.
// Database errors are returned unchanged.
func (s *Users) RemoveFavourite(ctx context.Context, userID, supplierID uuid.UUID) error {
	return s.db.WithContext(ctx).
		Delete(&models.FavouriteSupplier{}, "user_id = ? AND supplier_id = ?", userID, supplierID).Error
}
