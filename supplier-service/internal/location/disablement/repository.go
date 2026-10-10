package disablement

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
)

// Repository serializes a unit of work and rolls it back on any error.
// Tx reads used for mutations lock the resource until the unit of work commits.
type Repository interface {
	Within(context.Context, func(Tx) error) error
}
type Tx interface {
	Location(context.Context, string) (Location, error)
	Disablement(context.Context, string) (Disablement, error)
	SaveDisablement(context.Context, Disablement, int64) error
	Overlaps(context.Context, Disablement) (bool, error)
	ListDisablements(context.Context, string, DisablementState, time.Time, Page) ([]Disablement, int64, error)
	Idempotency() idempotency.Store
}
