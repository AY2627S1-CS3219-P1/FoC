package shared

import (
	"errors"
	"time"
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

type Disablement struct {
	ID       string
	StartsAt time.Time
	EndsAt   *time.Time
	Reason   string
}

var (
	ErrNotFound         = errors.New("location not found")
	ErrPermissionDenied = errors.New("permission denied")
)
