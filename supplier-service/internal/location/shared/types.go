package shared

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

type Location struct {
	ID                 string
	Name               string
	IsSupplier         bool
	Building           Building
	Categories         []Category
	Floor              *string
	Coordinates        Coordinates
	OpensAt            *Clock
	ClosesAt           *Clock
	Contact            *string
	Details            string
	ArchivedAt         *time.Time
	CurrentDisablement *Disablement
	Revision           int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Clock is an Asia/Singapore wall-clock time of day.
type Clock struct {
	Hour   int32
	Minute int32
}

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

type Building struct {
	ID      string
	Name    string
	Center  Coordinates
	RadiusM float32
}

type Category struct {
	ID   string
	Name string
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

var (
	ErrNotFound         = errs.NewNotFoundError("location not found")
	ErrPermissionDenied = errs.NewForbiddenError("permission denied")
)

type Caller struct{ ID, Role string }

func (c Caller) IsAdmin() bool { return c.Role == "admin" || c.Role == "super_admin" }
func (c Caller) Authenticated() bool {
	return c.ID != "" && (c.Role == "user" || c.Role == "suspended_user" || c.IsAdmin())
}

type Page struct{ Number, Size int32 }
type PageInfo struct {
	Page
	TotalItems int64
	TotalPages int32
}
