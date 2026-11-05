package warning

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("warning not found")
	// ErrUnknownCourier reports an insert rejected because the warned courier
	// has no row in users. The courier-warning consumer acknowledges such
	// events instead of retrying them.
	ErrUnknownCourier = errors.New("courier has no user account")
)

type Repository interface {
	Create(context.Context, Warning) (bool, error)
	ListByUser(context.Context, uuid.UUID) ([]Warning, error)
}
