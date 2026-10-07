package additionrequest

import (
	"time"

	shared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
)

var (
	ErrInvalidArgument    = shared.ErrInvalidArgument
	ErrUnauthenticated    = shared.ErrUnauthenticated
	ErrPermissionDenied   = shared.ErrAccessDenied
	ErrNotFound           = shared.ErrResourceNotFound
	ErrFailedPrecondition = shared.ErrFailedPrecondition
	ErrAlreadyExists      = shared.ErrAlreadyExists
	ErrAborted            = shared.ErrAborted
)

type Caller = shared.Caller
type Page = shared.Page
type PageInfo = shared.PageInfo
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
	CurrentDisablement   *shared.Disablement
	Revision             int64
	CreatedAt, UpdatedAt time.Time
}
type RequestPage struct {
	Items    []AdditionRequest
	PageInfo PageInfo
}
type Approval struct {
	Request  AdditionRequest
	Location Location
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
