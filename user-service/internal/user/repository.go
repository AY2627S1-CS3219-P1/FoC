package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"user-service/internal/models"
)

// Repository is the persistence boundary; the service depends on this
// interface so it can be unit-tested without a database.
type Repository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	List(ctx context.Context, p ListParams) ([]User, int64, error)
	Update(ctx context.Context, u *User) error
	Delete(ctx context.Context, id uuid.UUID) error

	ListFavourites(ctx context.Context, userID uuid.UUID) ([]models.FavouriteSupplier, error)
	AddFavourite(ctx context.Context, userID, supplierID uuid.UUID, now time.Time) error // idempotent
	RemoveFavourite(ctx context.Context, userID, supplierID uuid.UUID) error             // idempotent
}

type gormRepository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

func (r *gormRepository) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Take(&u, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *gormRepository) List(ctx context.Context, p ListParams) ([]User, int64, error) {
	var (
		users []User
		total int64
	)
	q := r.db.WithContext(ctx).Model(&User{})
	if p.Role != nil {
		q = q.Where("role = ?", *p.Role)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC, id").Limit(p.Limit).Offset(p.Offset).Find(&users).Error
	return users, total, err
}

// Update writes the mutable profile columns only; Select makes GORM persist
// NULLs (e.g. clearing a phone number).
func (r *gormRepository) Update(ctx context.Context, u *User) error {
	res := r.db.WithContext(ctx).Model(u).
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

func (r *gormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&User{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormRepository) ListFavourites(ctx context.Context, userID uuid.UUID) ([]models.FavouriteSupplier, error) {
	var favs []models.FavouriteSupplier
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&favs).Error
	return favs, err
}

func (r *gormRepository) AddFavourite(ctx context.Context, userID, supplierID uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.FavouriteSupplier{UserID: userID, SupplierID: supplierID, CreatedAt: now}).Error
}

func (r *gormRepository) RemoveFavourite(ctx context.Context, userID, supplierID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Delete(&models.FavouriteSupplier{}, "user_id = ? AND supplier_id = ?", userID, supplierID).Error
}
