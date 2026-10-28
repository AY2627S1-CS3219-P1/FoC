package seed

import (
	"context"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

type FixtureStore interface {
	UserMatches(context.Context, uuid.UUID, string) ([]models.User, error)
	RestoreUser(context.Context, models.User) error
	EnsureDomain(context.Context, models.AllowedEmailDomain) error
	EnsureRoleChange(context.Context, models.RoleChange) error
}

// Transaction contains adapters bound to the same serialized fixture transaction.
type Transaction struct {
	Users    service.UserCreator
	Events   service.UserCreatedEventStore
	Fixtures FixtureStore
}

type Seeder struct {
	WithTransaction func(context.Context, func(Transaction) error) error
}

func (s Seeder) Run(ctx context.Context, fixtures Fixtures, reset bool) error {
	if err := fixtures.Validate(); err != nil {
		return err
	}
	return s.WithTransaction(ctx, func(tx Transaction) error {
		provisioner := service.UserProvisioner{Users: tx.Users, Events: tx.Events}
		for _, fixture := range fixtures.Users {
			if err := seedUser(ctx, tx.Fixtures, provisioner, fixture, reset); err != nil {
				return fmt.Errorf("seed user %s: %w", fixture.ID, err)
			}
		}
		if err := tx.Fixtures.EnsureDomain(ctx, models.AllowedEmailDomain{
			BaseModel: models.BaseModel{ID: fixtures.Domain.ID}, Domain: fixtures.Domain.Domain,
		}); err != nil {
			return err
		}
		audit := fixtures.Suspension
		return tx.Fixtures.EnsureRoleChange(ctx, models.RoleChange{
			BaseModel:  models.BaseModel{ID: audit.ID},
			Userstamps: models.Userstamps{CreatedBy: &audit.ActorID, UpdatedBy: &audit.ActorID},
			UserID:     audit.UserID, FromRole: models.RoleUser, ToRole: models.RoleSuspended, Reason: &audit.Reason,
		})
	})
}

func seedUser(
	ctx context.Context,
	persistence FixtureStore,
	provisioner service.UserProvisioner,
	fixture User,
	reset bool,
) error {
	matches, err := persistence.UserMatches(ctx, fixture.ID, fixture.Email)
	if err != nil {
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
		return persistence.RestoreUser(ctx, candidate)
	}
	return nil
}
