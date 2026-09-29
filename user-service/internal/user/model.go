// Package user holds the user-profile rules. Persistence lives in
// internal/store; the logic added here declares the store methods it needs.
package user

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

// User is the shared entity from internal/models.
type User = models.User

var ErrSupplierNotFound = errors.New("supplier not found")

// UpdateInput is a partial update: nil = leave unchanged.
// For TelegramHandle / PhoneNumber, a pointer to "" clears the value.
// Email and Role are not updatable here (role changes live in admin).
type UpdateInput struct {
	DisplayName    *string
	Description    *string
	TelegramHandle *string
	PhoneNumber    *string
}

// SupplierChecker validates supplier IDs against the supplier service
// before favouriting (U3.4). Replace AllowAllSuppliers once that service
// exists.
type SupplierChecker interface {
	SupplierExists(ctx context.Context, id uuid.UUID) (bool, error)
}

type AllowAllSuppliers struct{}

// SupplierExists accepts every supplier ID without contacting the supplier service
// and always returns true, nil.
func (AllowAllSuppliers) SupplierExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
