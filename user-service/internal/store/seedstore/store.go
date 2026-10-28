package seedstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) WithTransaction(ctx context.Context, operation func(*store.Store, *Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
			return err
		}
		// Serialize concurrent seed invocations.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(18003219, 1)").Error; err != nil {
			return err
		}
		return operation(store.New(tx), New(tx))
	})
}

func (s *Store) UserMatches(ctx context.Context, id uuid.UUID, email string) ([]models.User, error) {
	var matches []models.User
	err := s.db.WithContext(ctx).Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? OR (email = ? AND deleted_at IS NULL)", id, email).
		Find(&matches).Error
	return matches, err
}

func (s *Store) RestoreUser(ctx context.Context, fixture models.User) error {
	return s.db.WithContext(ctx).Unscoped().Model(&models.User{}).Where("id = ?", fixture.ID).
		Updates(map[string]any{
			"display_name": fixture.DisplayName, "description": fixture.Description,
			"phone_number": fixture.PhoneNumber, "role": fixture.Role, "deleted_at": nil,
		}).Error
}

func (s *Store) EnsureDomain(ctx context.Context, fixture models.AllowedEmailDomain) error {
	db := s.db.WithContext(ctx)
	var active models.AllowedEmailDomain
	err := db.Where("domain = ?", fixture.Domain).Take(&active).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var existing models.AllowedEmailDomain
	err = db.Unscoped().Take(&existing, "id = ?", fixture.ID).Error
	if err == nil {
		if !strings.EqualFold(existing.Domain, fixture.Domain) {
			return fmt.Errorf("fixture registration domain identity conflicts with an existing domain")
		}
		return db.Unscoped().Model(&existing).Update("deleted_at", nil).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.Create(&fixture).Error
}

func (s *Store) EnsureRoleChange(ctx context.Context, change models.RoleChange) error {
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
		Create(&change).Error
}
