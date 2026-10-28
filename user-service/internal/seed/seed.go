package seed

import (
	"context"
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
		// Serialize concurrent seed invocations.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(18003219, 1)").Error; err != nil {
			return err
		}
		provisioner := service.UserProvisioner{Users: store.NewUsers(tx), Events: store.NewUserCreatedOutbox(tx)}
		for _, fixture := range fixtures.Users {
			if err := seedUser(ctx, tx, provisioner, fixture, reset); err != nil {
				return fmt.Errorf("seed user %s: %w", fixture.ID, err)
			}
		}
		if err := ensureDomain(ctx, tx, models.AllowedEmailDomain{
			BaseModel: models.BaseModel{ID: fixtures.Domain.ID}, Domain: fixtures.Domain.Domain,
		}); err != nil {
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

func seedUser(
	ctx context.Context,
	tx *gorm.DB,
	provisioner service.UserProvisioner,
	fixture User,
	reset bool,
) error {
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
	candidate := models.User{
		BaseModel: models.BaseModel{ID: fixture.ID}, Email: fixture.Email,
		DisplayName: fixture.DisplayName, Description: fixture.Description,
		PhoneNumber: fixture.PhoneNumber, Role: fixture.Role,
	}
	if len(matches) == 0 {
		return provisioner.Create(ctx, &candidate)
	}
	if reset {
		return tx.Unscoped().Model(&models.User{}).Where("id = ?", fixture.ID).
			Updates(map[string]any{
				"display_name": fixture.DisplayName, "description": fixture.Description,
				"phone_number": fixture.PhoneNumber, "role": fixture.Role, "deleted_at": nil,
			}).Error
	}
	return nil
}
