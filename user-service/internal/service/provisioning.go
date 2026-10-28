package service

import (
	"context"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	userdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/user"
	"github.com/google/uuid"
)

type UserCreator interface {
	Create(context.Context, *models.User) error
}

// UserProvisioner records a user and the fact consumed by Credit. Its stores
// must belong to the caller's transaction so a failed event rolls back the user.
type UserProvisioner struct {
	Users  UserCreator
	Events UserCreatedEventStore
}

func (p UserProvisioner) Create(ctx context.Context, user *models.User) error {
	if err := p.Users.Create(ctx, user); err != nil {
		return err
	}
	if err := p.Events.Append(ctx, userdomain.CreatedEvent{
		EventID: uuid.New(), UserID: user.ID, Email: user.Email,
		DisplayName: user.DisplayName, CreatedAt: user.CreatedAt,
	}); err != nil {
		return fmt.Errorf("write user-created outbox event: %w", err)
	}
	return nil
}
