package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Seeder struct {
	DB *gorm.DB
}

func (s Seeder) Run(ctx context.Context, fixtures Fixtures, reset bool) error {
	if err := fixtures.Validate(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
			return err
		}
		// Serialize with other seed invocations, including the former SQL seed.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(18003219, 1)").Error; err != nil {
			return err
		}
		persistence := store.New(tx)
		provisioner := service.UserProvisioner{Users: persistence.Users, Events: persistence.UserCreatedOutbox}
		for _, fixture := range fixtures.Users {
			if err := seedUser(ctx, tx, provisioner, fixture, reset); err != nil {
				return fmt.Errorf("seed user %s: %w", fixture.ID, err)
			}
		}
		if err := seedDomain(tx, fixtures.Domain); err != nil {
			return err
		}
		audit := fixtures.Suspension
		change := models.RoleChange{
			BaseModel:  models.BaseModel{ID: audit.ID},
			Userstamps: models.Userstamps{CreatedBy: &audit.ActorID, UpdatedBy: &audit.ActorID},
			UserID:     audit.UserID, FromRole: models.RoleUser, ToRole: models.RoleSuspended, Reason: &audit.Reason,
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
			Create(&change).
			Error
	})
}

func seedUser(ctx context.Context, tx *gorm.DB, provisioner service.UserProvisioner, fixture User, reset bool) error {
	var matches []models.User
	if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? OR (email = ? AND deleted_at IS NULL)", fixture.ID, fixture.Email).
		Find(&matches).Error; err != nil {
		return err
	}
	for _, match := range matches {
		if match.ID != fixture.ID || !strings.EqualFold(match.Email, fixture.Email) {
			return fmt.Errorf("fixture identity conflicts with an existing user")
		}
	}
	if len(matches) == 0 {
		user := models.User{
			BaseModel: models.BaseModel{ID: fixture.ID}, Email: fixture.Email,
			DisplayName: fixture.DisplayName, Description: fixture.Description,
			PhoneNumber: fixture.PhoneNumber, Role: fixture.Role,
		}
		return provisioner.Create(ctx, &user)
	}
	user := matches[0]
	if reset {
		if err := tx.Unscoped().Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"display_name": fixture.DisplayName, "description": fixture.Description,
			"phone_number": fixture.PhoneNumber, "role": fixture.Role, "deleted_at": nil,
		}).Error; err != nil {
			return err
		}
		// Scan into a fresh value so SQL NULL clears a previously set DeletedAt.
		user = models.User{}
		if err := tx.Unscoped().Take(&user, "id = ?", fixture.ID).Error; err != nil {
			return err
		}
	}
	if user.DeletedAt.Valid {
		return nil
	}
	return provisioner.RecordCreation(ctx, user)
}

func seedDomain(tx *gorm.DB, fixture Domain) error {
	var active models.AllowedEmailDomain
	err := tx.Where("domain = ?", fixture.Domain).Take(&active).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var existing models.AllowedEmailDomain
	err = tx.Unscoped().Take(&existing, "id = ?", fixture.ID).Error
	if err == nil {
		if !strings.EqualFold(existing.Domain, fixture.Domain) {
			return fmt.Errorf("fixture registration domain identity conflicts with an existing domain")
		}
		return tx.Unscoped().Model(&existing).Update("deleted_at", nil).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	domain := models.AllowedEmailDomain{BaseModel: models.BaseModel{ID: fixture.ID}, Domain: fixture.Domain}
	return tx.Create(&domain).Error
}
