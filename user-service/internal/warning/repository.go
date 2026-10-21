package warning

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("warning not found")

type Repository interface {
	Create(context.Context, Warning) (bool, error)
	ListByUser(context.Context, uuid.UUID) ([]Warning, error)
}
