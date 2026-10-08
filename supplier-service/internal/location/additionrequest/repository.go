package additionrequest

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
	Request(context.Context, string) (AdditionRequest, error)
	SaveRequest(context.Context, AdditionRequest, int64) error
	ListRequests(context.Context, Caller, RequestStatus, Page) ([]AdditionRequest, int64, error)
	ValidateReferences(context.Context, Proposal) error
	CreateLocation(context.Context, Proposal, time.Time) (Location, error)
	Idempotency() idempotency.Store
}
