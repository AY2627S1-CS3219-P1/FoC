package warning

import (
	"context"
	"errors"
	"time"

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
	ListByUser(context.Context, uuid.UUID, int64, int64) ([]Warning, error)
	ListForAdmin(context.Context, uuid.UUID, int64, int64) ([]AdminWarning, error)
	CountByUser(context.Context, uuid.UUID) (int64, error)
	Remove(context.Context, uuid.UUID, time.Time, *string, *uuid.UUID, *uuid.UUID) (Warning, error)
}
