package disablement

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

type Disablement = shared.Disablement
type DisablementState = shared.DisablementState

const (
	Scheduled = shared.Scheduled
	Active    = shared.Active
	Ended     = shared.Ended
	Cancelled = shared.Cancelled
)

type Location struct {
	ID         string
	ArchivedAt *time.Time
}
type DisablementPage struct {
	Items    []Disablement
	PageInfo PageInfo
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
