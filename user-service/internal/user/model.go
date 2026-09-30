// Package user holds the user-profile rules; persistence lives in internal/store.
package user

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type User = models.User

var ErrSupplierNotFound = errors.New("supplier not found")

type SupplierChecker interface {
	SupplierExists(ctx context.Context, id uuid.UUID) (bool, error)
}

// AllowAllSuppliers is a placeholder until the supplier service can be queried.
type AllowAllSuppliers struct{}

func (AllowAllSuppliers) SupplierExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
