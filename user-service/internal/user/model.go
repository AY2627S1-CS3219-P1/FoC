package user

import (
	"context"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/models"
)

// User is the shared entity from internal/models.
type User = models.User

var (
	ErrNotFound         = apperr.ErrNotFound.WithMessage("user not found")
	ErrSupplierNotFound = apperr.ErrNotFound.WithMessage("supplier not found")
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

// Normalize clamps paging params to sane bounds.
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

func (AllowAllSuppliers) SupplierExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
