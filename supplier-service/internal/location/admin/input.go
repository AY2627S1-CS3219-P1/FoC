package admin

import (
	shared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Input contains exactly the fields an administrator may write.
type Input struct {
	Name        string
	IsSupplier  bool
	CategoryIDs []string
	BuildingID  string
	Floor       *string
	Coordinates *shared.Coordinates
	OpensAt     *shared.Clock
	ClosesAt    *shared.Clock
	Contact     *string
	Details     string
}

func canonicalID(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", ErrInvalidArgument
	}
	return id.String(), nil
}

func normalizeInput(in Input) (Input, error) {
	in.CategoryIDs = slices.Clone(in.CategoryIDs)
	if len(in.CategoryIDs) == 0 {
		in.CategoryIDs = nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		return Input{}, ErrInvalidArgument
	}
	in.Details = strings.TrimSpace(in.Details)
	if utf8.RuneCountInString(in.Details) > 2000 {
		return Input{}, ErrInvalidArgument
	}
	var err error
	in.Floor, err = normalizeOptional(in.Floor, 50)
	if err != nil {
		return Input{}, err
	}
	in.Contact, err = normalizeOptional(in.Contact, 500)
	if err != nil {
		return Input{}, err
	}
	in.BuildingID, err = canonicalID(in.BuildingID)
	if err != nil {
		return Input{}, err
	}
	seen := make(map[string]bool, len(in.CategoryIDs))
	for i, raw := range in.CategoryIDs {
		id, e := canonicalID(raw)
		if e != nil || seen[id] {
			return Input{}, ErrInvalidArgument
		}
		seen[id] = true
		in.CategoryIDs[i] = id
	}
	if in.IsSupplier != (len(in.CategoryIDs) > 0) {
		return Input{}, ErrFailedPrecondition
	}
	if in.Coordinates == nil || !validCoordinates(*in.Coordinates) {
		return Input{}, ErrInvalidArgument
	}
	if (in.OpensAt == nil) != (in.ClosesAt == nil) {
		return Input{}, ErrInvalidArgument
	}
	if in.OpensAt != nil {
		if !validClock(*in.OpensAt) || !validClock(*in.ClosesAt) || *in.OpensAt == *in.ClosesAt {
			return Input{}, ErrInvalidArgument
		}
	}
	return in, nil
}

func normalizeOptional(p *string, maxLen int) (*string, error) {
	if p == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*p)
	if utf8.RuneCountInString(v) > maxLen {
		return nil, ErrInvalidArgument
	}
	if v == "" {
		return nil, nil
	}
	return &v, nil
}

func validCoordinates(c shared.Coordinates) bool {
	return !math.IsNaN(c.Latitude) && !math.IsInf(c.Latitude, 0) && c.Latitude >= -90 && c.Latitude <= 90 &&
		!math.IsNaN(c.Longitude) && !math.IsInf(c.Longitude, 0) && c.Longitude >= -180 && c.Longitude <= 180
}

func validClock(c shared.Clock) bool {
	return c.Hour >= 0 && c.Hour < 24 && c.Minute >= 0 && c.Minute < 60
}
