// Package workflows owns temporary disablements and Location-addition requests.
// Transport and persistence representations remain outside this package.
package workflows

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrNotFound           = errors.New("resource not found")
	ErrFailedPrecondition = errors.New("invalid state")
	ErrAlreadyExists      = errors.New("conflict")
	ErrAborted            = errors.New("stale revision")
)

type Caller struct{ ID, Role string }

func (c Caller) IsAdmin() bool { return c.Role == "admin" || c.Role == "super_admin" }
func (c Caller) Authenticated() bool {
	return c.ID != "" && (c.Role == "user" || c.Role == "suspended_user" || c.IsAdmin())
}

type DisablementState string

const (
	Scheduled DisablementState = "scheduled"
	Active    DisablementState = "active"
	Ended     DisablementState = "ended"
	Cancelled DisablementState = "cancelled"
)

type Disablement struct {
	ID, LocationID               string
	StartsAt                     time.Time
	EndsAt, EndedAt, CancelledAt *time.Time
	Reason, CreatedBy            string
	Revision                     int64
	CreatedAt, UpdatedAt         time.Time
}

func (d Disablement) State(now time.Time) DisablementState {
	if d.CancelledAt != nil {
		return Cancelled
	}
	if d.EndedAt != nil || (d.EndsAt != nil && !now.Before(*d.EndsAt)) {
		return Ended
	}
	if now.Before(d.StartsAt) {
		return Scheduled
	}
	return Active
}

type RequestStatus string

const (
	Pending   RequestStatus = "pending"
	Approved  RequestStatus = "approved"
	Rejected  RequestStatus = "rejected"
	Withdrawn RequestStatus = "withdrawn"
)

// Opening times are microseconds since local midnight in Asia/Singapore.
// PostgreSQL TIME has microsecond precision; wire validation rejects finer times.
type Proposal struct {
	Name                string
	IsSupplier          bool
	CategoryIDs         []string
	BuildingID          string
	Floor               *string
	Latitude, Longitude float64
	// Preserves absence in incomplete proposals from migration 00008.
	CoordinatesMissing bool
	OpenFrom, OpenTo   *int64
	Contact            *string
	Details            string
}
type AdditionRequest struct {
	ID                              string
	Proposal                        Proposal
	SubmittedBy                     string
	Status                          RequestStatus
	ReviewedBy                      *string
	ReviewedAt                      *time.Time
	ReviewNote, ResultingLocationID *string
	Revision                        int64
	CreatedAt, UpdatedAt            time.Time
}
type Building struct {
	ID, Name             string
	Latitude, Longitude  float64
	RadiusM              float32
	CreatedAt, UpdatedAt time.Time
}
type Category struct {
	ID, Name  string
	CreatedAt time.Time
}
type Location struct {
	ID                   string
	Proposal             Proposal
	Building             Building
	Categories           []Category
	ArchivedAt           *time.Time
	CurrentDisablement   *Disablement
	Revision             int64
	CreatedAt, UpdatedAt time.Time
}
type Page struct{ Number, Size int32 }
type PageInfo struct {
	Page
	TotalItems int64
	TotalPages int32
}
type DisablementPage struct {
	Items    []Disablement
	PageInfo PageInfo
}
type RequestPage struct {
	Items    []AdditionRequest
	PageInfo PageInfo
}
type Approval struct {
	Request  AdditionRequest
	Location Location
}
type CreateDisablement struct {
	LocationID       string
	StartsAt, EndsAt *time.Time
	Reason, Key      string
}
type UpdateDisablement struct {
	ID               string
	StartsAt, EndsAt *time.Time
	Reason           string
	Paths            []string
	ExpectedRevision int64
}
type SubmitRequest struct {
	Proposal Proposal
	Key      string
}
type UpdateRequest struct {
	ID               string
	Proposal         Proposal
	Paths            []string
	ExpectedRevision int64
}
type IdempotencyScope struct{ Caller, Method, Key string }
type IdempotencyRecord struct {
	Hash, ResourceID string
	ExpiresAt        time.Time
}

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
