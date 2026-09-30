package lifecycle

import (
	"context"
	"time"
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
	Request(context.Context, string) (AdditionRequest, error)
	SaveRequest(context.Context, AdditionRequest, int64) error
	ListRequests(context.Context, Caller, RequestStatus, Page) ([]AdditionRequest, int64, error)
	ValidateReferences(context.Context, Proposal) error
	CreateLocation(context.Context, Proposal, time.Time) (Location, error)
	Idempotency(context.Context, IdempotencyScope, time.Time) (*IdempotencyRecord, error)
	SaveIdempotency(context.Context, IdempotencyScope, IdempotencyRecord) error
}
