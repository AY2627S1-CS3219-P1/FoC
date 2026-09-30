// Package workflows owns temporary disablements and Location-addition requests.
// Transport and persistence representations remain outside this package.
package lifecycle

import (
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

// Caller contains identity and role supplied by a trusted authentication layer.
// Application rules check access but do not verify credentials.
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
