package warning

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("warning not found")

type Repository interface {
	Create(context.Context, Warning) (bool, error)
	GetByEventID(context.Context, uuid.UUID) (Warning, error)
	GetByID(context.Context, uuid.UUID) (Warning, error)
	ListByUser(context.Context, uuid.UUID) ([]Warning, error)
	Remove(context.Context, uuid.UUID, time.Time, *string, *uuid.UUID, *uuid.UUID) (Warning, error)
}
