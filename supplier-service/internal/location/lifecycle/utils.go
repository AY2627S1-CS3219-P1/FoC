package lifecycle

import (
	"math"
	"sort"

	"github.com/google/uuid"
)

func normalizeProposal(p Proposal) (Proposal, error) {
	var err error
	p.Name, err = trimLimit(p.Name, 1, 200)
	if err != nil {
		return p, err
	}
	p.Details, err = trimLimit(p.Details, 0, 2000)
	if err != nil {
		return p, err
	}
	for _, f := range []struct {
		v   **string
		max int
	}{{&p.Floor, 50}, {&p.Contact, 500}} {
		if *f.v != nil {
			s, e := trimLimit(**f.v, 0, f.max)
			if e != nil {
				return p, e
			}
			if s == "" {
				*f.v = nil
			} else {
				*f.v = &s
			}
		}
	}
	if err = validID(p.BuildingID); err != nil {
		return p, err
	}
	p.BuildingID = uuid.MustParse(p.BuildingID).String()
	if p.CoordinatesMissing || !isValidCoordinates(p.Latitude, p.Longitude) {
		return p, ErrInvalidArgument
	}
	if !isValidOpeningHours(p.OpenFrom, p.OpenTo) {
		return p, ErrInvalidArgument
	}
	p.CategoryIDs = append([]string(nil), p.CategoryIDs...)
	for i, id := range p.CategoryIDs {
		if validID(id) != nil {
			return p, ErrInvalidArgument
		}
		p.CategoryIDs[i] = uuid.MustParse(id).String()
	}
	sort.Strings(p.CategoryIDs)
	for i, id := range p.CategoryIDs {
		if i > 0 && id == p.CategoryIDs[i-1] {
			return p, ErrInvalidArgument
		}
	}
	if !p.IsSupplier {
		p.CategoryIDs = nil
	} else if len(p.CategoryIDs) == 0 {
		return p, ErrFailedPrecondition
	}
	return p, nil
}

func isValidCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) && latitude >= -90 && latitude <= 90 &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0) && longitude >= -180 && longitude <= 180
}

func isValidOpeningHours(from, to *int64) bool {
	const microsecondsPerDay = 86400000000
	if from == nil || to == nil {
		return from == nil && to == nil
	}
	return *from >= 0 && *from < microsecondsPerDay && *to >= 0 && *to < microsecondsPerDay && *from != *to
}

func applyProposalPatch(proposal, patch Proposal, fields map[string]bool) Proposal {
	if fields["name"] {
		proposal.Name = patch.Name
	}
	if fields["is_supplier"] {
		proposal.IsSupplier = patch.IsSupplier
	}
	if fields["category_ids"] {
		proposal.CategoryIDs = patch.CategoryIDs
	}
	if fields["building_id"] {
		proposal.BuildingID = patch.BuildingID
	}
	if fields["floor"] {
		proposal.Floor = patch.Floor
	}
	if fields["coordinates"] {
		proposal.CoordinatesMissing = patch.CoordinatesMissing
		proposal.Latitude = patch.Latitude
		proposal.Longitude = patch.Longitude
	}
	if fields["open_from"] {
		proposal.OpenFrom = patch.OpenFrom
		proposal.OpenTo = patch.OpenTo
	}
	if fields["contact"] {
		proposal.Contact = patch.Contact
	}
	if fields["details"] {
		proposal.Details = patch.Details
	}
	return proposal
}

func isVisible(c Caller, r AdditionRequest) bool {
	return c.IsAdmin() || r.SubmittedBy == c.ID || r.Status == Approved
}

func redactRequestForCaller(c Caller, r AdditionRequest) AdditionRequest {
	if !c.IsAdmin() && r.SubmittedBy != c.ID {
		r.SubmittedBy = ""
		r.ReviewedBy = nil
		r.ReviewNote = nil
	}
	return r
}
