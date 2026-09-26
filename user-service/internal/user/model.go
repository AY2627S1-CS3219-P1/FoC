package user

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

// User is the shared entity from internal/models.
type User = models.User

var (
	ErrNotFound         = errors.New("user not found")
	ErrSupplierNotFound = errors.New("supplier not found")
	// ErrInUse: the user is still referenced with ON DELETE RESTRICT
	// (e.g. the admin_bootstrap row), so it cannot be deleted.
	ErrInUse = errors.New("user cannot be deleted")
)

// UpdateInput is a partial update: nil = leave unchanged.
// For TelegramHandle / PhoneNumber, a pointer to "" clears the value.
// Email and Role are not updatable here (role changes live in admin).
type UpdateInput struct {
	DisplayName    *string
	Description    *string
	TelegramHandle *string
	PhoneNumber    *string
}

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
