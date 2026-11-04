package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Seeder struct {
	DB *gorm.DB
}

// Concurrent seed invocations against one database are serialized with a
// Postgres transaction advisory lock. The two int32 parts form a key that is
// unique to this service so unrelated locks cannot stall the seed.
const (
	seedAdvisoryLockClass int32 = 18003219
	seedAdvisoryLockID    int32 = 1
)

func (s Seeder) Run(ctx context.Context, fixtures Fixtures, reset bool) error {
	if err := fixtures.Validate(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
			return err
		}
		// Serialize concurrent seed invocations.
		if err := tx.Exec(fmt.Sprintf(
			"SELECT pg_advisory_xact_lock(%d, %d)", seedAdvisoryLockClass, seedAdvisoryLockID,
		)).Error; err != nil {
			return err
		}
		provisioner := service.UserProvisioner{Users: store.NewUsers(tx), Events: store.NewUserCreatedOutbox(tx)}
		suspended := fixtures.Suspension.UserID
		suspensionTouched := false
		for _, fixture := range fixtures.Users {
			touched, err := seedUser(ctx, tx, provisioner, fixture, suspended, reset)
			if err != nil {
				return fmt.Errorf("seed user %s: %w", fixture.ID, err)
			}
			suspensionTouched = suspensionTouched || (touched && fixture.ID == suspended)
		}
		if err := ensureDomain(tx, models.AllowedEmailDomain{
			BaseModel: models.BaseModel{ID: fixtures.Domain.ID}, Domain: fixtures.Domain.Domain,
		}); err != nil {
			return err
		}
		if !suspensionTouched {
			return nil
		}
		// Only rerun-created or reset targets are re-suspended, so a user an
		// operator reinstated between seed runs stays reinstated.
		return suspendFixtureUser(ctx, tx, fixtures.Suspension)
	})
}

// suspendFixtureUser applies the fixture suspension through the role-change
// application logic so the audit row and user update match production.
func suspendFixtureUser(ctx context.Context, tx *gorm.DB, suspension Suspension) error {
	roles := &service.RoleService{
		Users: store.NewUsers(tx),
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return operation(service.RoleStore{Users: store.NewUsers(tx), Admin: store.NewAdmin(tx)})
		},
	}
	if _, err := roles.ChangeUserRole(
		ctx, suspension.ActorID, suspension.UserID, models.RoleSuspended, suspension.Reason,
	); err != nil && !errors.Is(err, service.ErrRoleUnchanged) {
		return err
	}
	return nil
}

func seedUser(
	ctx context.Context,
	tx *gorm.DB,
	provisioner service.UserProvisioner,
	fixture User,
	suspended uuid.UUID,
	reset bool,
) (bool, error) {
	var matches []models.User
	if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? OR (email = ? AND deleted_at IS NULL)", fixture.ID, fixture.Email).
		Find(&matches).Error; err != nil {
		return false, err
	}
	for _, match := range matches {
		if match.ID != fixture.ID || !strings.EqualFold(match.Email, fixture.Email) {
			return false, fmt.Errorf("fixture identity conflicts with an existing user")
		}
	}
	// The suspension fixture target starts at the ordinary role so
	// suspendFixtureUser performs the recorded user -> suspended transition.
	role := fixture.Role
	if fixture.ID == suspended {
		role = models.RoleUser
	}
	candidate := models.User{
		BaseModel: models.BaseModel{ID: fixture.ID}, Email: fixture.Email,
		DisplayName: fixture.DisplayName, Description: fixture.Description,
		PhoneNumber: fixture.PhoneNumber, Role: role,
	}
	if len(matches) == 0 {
		return true, provisioner.Create(ctx, &candidate)
	}
	if reset {
		return true, tx.Unscoped().Model(&models.User{}).Where("id = ?", fixture.ID).
			Updates(map[string]any{
				"display_name": fixture.DisplayName, "description": fixture.Description,
				"phone_number": fixture.PhoneNumber, "role": role, "deleted_at": nil,
			}).Error
	}
	return false, nil
}
