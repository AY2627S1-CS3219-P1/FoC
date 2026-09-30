package workflows

import (
	"context"
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

func (s *Service) SubmitRequest(ctx context.Context, c Caller, in SubmitRequest) (out AdditionRequest, err error) {
	if err = requireAuthenticated(c); err != nil {
		return
	}
	if c.Role == "suspended_user" {
		return out, ErrPermissionDenied
	}
	in.Proposal, err = normalizeProposal(in.Proposal)
	if err != nil {
		return
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		now := s.clock().UTC()
		id, e := idempotent(ctx, tx, c, "SubmitLocationAdditionRequest", in.Key, in.Proposal, now, func() (string, error) {
			if e := tx.ValidateReferences(ctx, in.Proposal); e != nil {
				return "", e
			}
			r := AdditionRequest{ID: uuid.NewString(), Proposal: in.Proposal, SubmittedBy: c.ID, Status: Pending, Revision: 1, CreatedAt: now, UpdatedAt: now}
			return r.ID, tx.SaveRequest(ctx, r, 0)
		})
		if e != nil {
			return e
		}
		out, e = tx.Request(ctx, id)
		return e
	})
	return
}

func (s *Service) GetRequest(ctx context.Context, c Caller, id string) (out AdditionRequest, err error) {
	if err = requireAuthenticated(c); err != nil {
		return
	}
	if err = validID(id); err != nil {
		return
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		r, e := tx.Request(ctx, id)
		if e != nil {
			return e
		}
		if !isVisible(c, r) {
			return ErrNotFound
		}
		out = redactRequestForCaller(c, r)
		return nil
	})
	return
}

func (s *Service) ListRequests(ctx context.Context, c Caller, status RequestStatus, p Page) (out RequestPage, err error) {
	if err = requireAuthenticated(c); err != nil {
		return
	}
	if status != "" && status != Pending && status != Approved && status != Rejected && status != Withdrawn {
		return out, ErrInvalidArgument
	}
	p, err = pagination(p)
	if err != nil {
		return
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		items, n, e := tx.ListRequests(ctx, c, status, p)
		for i := range items {
			items[i] = redactRequestForCaller(c, items[i])
		}
		out = RequestPage{Items: items, PageInfo: pageInfo(p, n)}
		return e
	})
	return
}

func (s *Service) UpdateRequest(ctx context.Context, c Caller, in UpdateRequest) (out AdditionRequest, err error) {
	if err = requireAuthenticated(c); err != nil {
		return
	}
	if validID(in.ID) != nil || in.ExpectedRevision <= 0 {
		return out, ErrInvalidArgument
	}
	m, e := mask(in.Paths, "name", "is_supplier", "category_ids", "building_id", "floor", "coordinates", "open_from", "open_to", "contact", "details")
	if e != nil {
		return out, e
	}
	if m["open_from"] != m["open_to"] {
		return out, ErrInvalidArgument
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		r, e := tx.Request(ctx, in.ID)
		if e != nil {
			return e
		}
		if !isVisible(c, r) {
			return ErrNotFound
		}
		if !c.IsAdmin() && c.ID != r.SubmittedBy {
			return ErrPermissionDenied
		}
		if r.Status != Pending {
			return ErrFailedPrecondition
		}
		if r.Revision != in.ExpectedRevision {
			return ErrAborted
		}
		proposal := applyProposalPatch(r.Proposal, in.Proposal, m)
		proposal, e = normalizeProposal(proposal)
		if e != nil {
			return e
		}
		if e = tx.ValidateReferences(ctx, proposal); e != nil {
			return e
		}
		r.Proposal = proposal
		r.Revision++
		r.UpdatedAt = s.clock().UTC()
		out = r
		return tx.SaveRequest(ctx, r, in.ExpectedRevision)
	})
	return
}

func (s *Service) WithdrawRequest(ctx context.Context, c Caller, id string) (AdditionRequest, error) {
	a, e := s.transitionRequest(ctx, c, id, Withdrawn, "")
	return a.Request, e
}

func (s *Service) RejectRequest(ctx context.Context, c Caller, id, note string) (AdditionRequest, error) {
	a, e := s.transitionRequest(ctx, c, id, Rejected, note)
	return a.Request, e
}

func (s *Service) ApproveRequest(ctx context.Context, c Caller, id string) (Approval, error) {
	return s.transitionRequest(ctx, c, id, Approved, "")
}

func (s *Service) transitionRequest(ctx context.Context, c Caller, id string, target RequestStatus, note string) (out Approval, err error) {
	if err = requireAuthenticated(c); err != nil {
		return
	}
	if target != Withdrawn && !c.IsAdmin() {
		return out, ErrPermissionDenied
	}
	if err = validID(id); err != nil {
		return
	}
	if target == Rejected {
		note, err = trimLimit(note, 1, 2000)
		if err != nil {
			return
		}
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		r, e := tx.Request(ctx, id)
		if e != nil {
			return e
		}
		if !isVisible(c, r) {
			return ErrNotFound
		}
		if target == Withdrawn && r.SubmittedBy != c.ID {
			return ErrPermissionDenied
		}
		if r.Status == target {
			out.Request = redactRequestForCaller(c, r)
			if target == Approved {
				out.Location, e = tx.Location(ctx, *r.ResultingLocationID)
			}
			return e
		}
		if r.Status != Pending {
			return ErrFailedPrecondition
		}
		now := s.clock().UTC()
		if target == Approved {
			r.Proposal, e = normalizeProposal(r.Proposal)
			if e != nil {
				return ErrFailedPrecondition
			}
			if e = tx.ValidateReferences(ctx, r.Proposal); e != nil {
				return e
			}
			out.Location, e = tx.CreateLocation(ctx, r.Proposal, now)
			if e != nil {
				return e
			}
			r.ResultingLocationID = &out.Location.ID
		}
		if target != Withdrawn {
			r.ReviewedBy = &c.ID
			r.ReviewedAt = &now
		}
		if target == Rejected {
			r.ReviewNote = &note
		}
		prev := r.Revision
		r.Status = target
		r.Revision++
		r.UpdatedAt = now
		out.Request = r
		return tx.SaveRequest(ctx, r, prev)
	})
	return
}
