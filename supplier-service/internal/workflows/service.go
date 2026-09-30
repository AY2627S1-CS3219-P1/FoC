package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Service struct {
	repo  Repository
	clock func() time.Time
}

func New(repo Repository, clock func() time.Time) *Service { return &Service{repo: repo, clock: clock} }
func authenticate(c Caller) error {
	if !c.Authenticated() {
		return ErrUnauthenticated
	}
	return nil
}
func administrator(c Caller) error {
	if err := authenticate(c); err != nil {
		return err
	}
	if !c.IsAdmin() {
		return ErrPermissionDenied
	}
	return nil
}
func validID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrInvalidArgument
	}
	return nil
}
func trimLimit(s string, min, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		return "", ErrInvalidArgument
	}
	return s, nil
}
func normalizeTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}
func validateDisablement(d *Disablement) error {
	var err error
	d.Reason, err = trimLimit(d.Reason, 1, 500)
	if err != nil {
		return err
	}
	d.StartsAt = d.StartsAt.UTC()
	d.EndsAt = normalizeTime(d.EndsAt)
	if d.EndsAt != nil && !d.EndsAt.After(d.StartsAt) {
		return ErrInvalidArgument
	}
	return nil
}
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
	if p.CoordinatesMissing || math.IsNaN(p.Latitude) || math.IsInf(p.Latitude, 0) || p.Latitude < -90 || p.Latitude > 90 || math.IsNaN(p.Longitude) || math.IsInf(p.Longitude, 0) || p.Longitude < -180 || p.Longitude > 180 {
		return p, ErrInvalidArgument
	}
	if (p.OpenFrom == nil) != (p.OpenTo == nil) {
		return p, ErrInvalidArgument
	}
	if p.OpenFrom != nil && (*p.OpenFrom < 0 || *p.OpenFrom >= 86400000000 || *p.OpenTo < 0 || *p.OpenTo >= 86400000000 || *p.OpenFrom == *p.OpenTo) {
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
func pagination(p Page) (Page, error) {
	if p.Number < 0 || p.Size < 0 || p.Size > 100 {
		return p, ErrInvalidArgument
	}
	if p.Number == 0 {
		p.Number = 1
	}
	if p.Size == 0 {
		p.Size = 20
	}
	return p, nil
}
func pageInfo(p Page, count int64) PageInfo {
	pages := (count + int64(p.Size) - 1) / int64(p.Size)
	if pages > math.MaxInt32 {
		pages = math.MaxInt32
	}
	return PageInfo{Page: p, TotalItems: count, TotalPages: int32(pages)}
}
func requestHash(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func idempotent(ctx context.Context, tx Tx, c Caller, method, key string, payload any, now time.Time, create func() (string, error)) (string, error) {
	if validID(key) != nil {
		return "", ErrInvalidArgument
	}
	hash, err := requestHash(payload)
	if err != nil {
		return "", err
	}
	scope := IdempotencyScope{Caller: c.ID, Method: method, Key: uuid.MustParse(key).String()}
	record, err := tx.Idempotency(ctx, scope, now)
	if err != nil {
		return "", err
	}
	if record != nil {
		if record.Hash != hash {
			return "", ErrAlreadyExists
		}
		return record.ResourceID, nil
	}
	id, err := create()
	if err != nil {
		return "", err
	}
	err = tx.SaveIdempotency(ctx, scope, IdempotencyRecord{Hash: hash, ResourceID: id, ExpiresAt: now.Add(24 * time.Hour)})
	return id, err
}
func (s *Service) CreateDisablement(ctx context.Context, c Caller, in CreateDisablement) (out Disablement, err error) {
	if err = administrator(c); err != nil {
		return
	}
	if err = validID(in.LocationID); err != nil {
		return
	}
	in.LocationID = uuid.MustParse(in.LocationID).String()
	// Hash the explicit input, not the clock-derived default start. A retry with
	// omitted start must remain identical when wall time has advanced.
	in.Reason, err = trimLimit(in.Reason, 1, 500)
	if err != nil {
		return
	}
	in.StartsAt = normalizeTime(in.StartsAt)
	in.EndsAt = normalizeTime(in.EndsAt)
	payload := struct {
		LocationID       string
		StartsAt, EndsAt *time.Time
		Reason           string
	}{in.LocationID, in.StartsAt, in.EndsAt, in.Reason}
	err = s.repo.Within(ctx, func(tx Tx) error {
		now := s.clock().UTC()
		id, e := idempotent(ctx, tx, c, "CreateDisablement", in.Key, payload, now, func() (string, error) {
			l, e := tx.Location(ctx, in.LocationID)
			if e != nil {
				return "", e
			}
			if l.ArchivedAt != nil {
				return "", ErrFailedPrecondition
			}
			d := Disablement{ID: uuid.NewString(), LocationID: in.LocationID, StartsAt: now, EndsAt: in.EndsAt, Reason: in.Reason, CreatedBy: c.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if in.StartsAt != nil {
				d.StartsAt = *in.StartsAt
			}
			if e = validateDisablement(&d); e != nil {
				return "", e
			}
			overlap, e := tx.Overlaps(ctx, d)
			if e != nil {
				return "", e
			}
			if overlap {
				return "", ErrAlreadyExists
			}
			return d.ID, tx.SaveDisablement(ctx, d, 0)
		})
		if e != nil {
			return e
		}
		out, e = tx.Disablement(ctx, id)
		return e
	})
	return
}
func (s *Service) ListDisablements(ctx context.Context, c Caller, locationID string, state DisablementState, p Page) (out DisablementPage, err error) {
	if err = administrator(c); err != nil {
		return
	}
	if err = validID(locationID); err != nil {
		return
	}
	if state != "" && state != Scheduled && state != Active && state != Ended && state != Cancelled {
		return out, ErrInvalidArgument
	}
	p, err = pagination(p)
	if err != nil {
		return
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		if _, e := tx.Location(ctx, locationID); e != nil {
			return e
		}
		items, n, e := tx.ListDisablements(ctx, locationID, state, s.clock().UTC(), p)
		out = DisablementPage{Items: items, PageInfo: pageInfo(p, n)}
		return e
	})
	return
}
func mask(paths []string, allowed ...string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}
	m := map[string]bool{}
	for _, p := range paths {
		ok := false
		for _, a := range allowed {
			if p == a {
				ok = true
			}
		}
		if !ok || m[p] {
			return nil, ErrInvalidArgument
		}
		m[p] = true
	}
	return m, nil
}
func (s *Service) UpdateDisablement(ctx context.Context, c Caller, in UpdateDisablement) (out Disablement, err error) {
	if err = administrator(c); err != nil {
		return
	}
	if validID(in.ID) != nil || in.ExpectedRevision <= 0 {
		return out, ErrInvalidArgument
	}
	m, e := mask(in.Paths, "starts_at", "ends_at", "reason")
	if e != nil {
		return out, e
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		d, e := tx.Disablement(ctx, in.ID)
		if e != nil {
			return e
		}
		if _, e = tx.Location(ctx, d.LocationID); e != nil {
			return e
		}
		now := s.clock().UTC()
		if d.State(now) != Scheduled {
			return ErrFailedPrecondition
		}
		if d.Revision != in.ExpectedRevision {
			return ErrAborted
		}
		if m["starts_at"] {
			if in.StartsAt == nil {
				return ErrInvalidArgument
			}
			d.StartsAt = *in.StartsAt
		}
		if m["ends_at"] {
			d.EndsAt = in.EndsAt
		}
		if m["reason"] {
			d.Reason = in.Reason
		}
		if e = validateDisablement(&d); e != nil {
			return e
		}
		overlap, e := tx.Overlaps(ctx, d)
		if e != nil {
			return e
		}
		if overlap {
			return ErrAlreadyExists
		}
		d.Revision++
		d.UpdatedAt = now
		out = d
		return tx.SaveDisablement(ctx, d, in.ExpectedRevision)
	})
	return
}
func (s *Service) EndDisablement(ctx context.Context, c Caller, id string) (Disablement, error) {
	return s.transitionDisablement(ctx, c, id, Ended)
}
func (s *Service) CancelDisablement(ctx context.Context, c Caller, id string) (Disablement, error) {
	return s.transitionDisablement(ctx, c, id, Cancelled)
}
func (s *Service) transitionDisablement(ctx context.Context, c Caller, id string, target DisablementState) (out Disablement, err error) {
	if err = administrator(c); err != nil {
		return
	}
	if err = validID(id); err != nil {
		return
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		d, e := tx.Disablement(ctx, id)
		if e != nil {
			return e
		}
		now := s.clock().UTC()
		state := d.State(now)
		if state == target {
			out = d
			return nil
		}
		if (target == Ended && state != Active) || (target == Cancelled && state != Scheduled) {
			return ErrFailedPrecondition
		}
		prev := d.Revision
		if target == Ended {
			d.EndedAt = &now
		} else {
			d.CancelledAt = &now
		}
		d.Revision++
		d.UpdatedAt = now
		out = d
		return tx.SaveDisablement(ctx, d, prev)
	})
	return
}
func visible(c Caller, r AdditionRequest) bool {
	return c.IsAdmin() || r.SubmittedBy == c.ID || r.Status == Approved
}
func project(c Caller, r AdditionRequest) AdditionRequest {
	if !c.IsAdmin() && r.SubmittedBy != c.ID {
		r.SubmittedBy = ""
		r.ReviewedBy = nil
		r.ReviewNote = nil
	}
	return r
}
func (s *Service) SubmitRequest(ctx context.Context, c Caller, in SubmitRequest) (out AdditionRequest, err error) {
	if err = authenticate(c); err != nil {
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
	if err = authenticate(c); err != nil {
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
		if !visible(c, r) {
			return ErrNotFound
		}
		out = project(c, r)
		return nil
	})
	return
}
func (s *Service) ListRequests(ctx context.Context, c Caller, status RequestStatus, p Page) (out RequestPage, err error) {
	if err = authenticate(c); err != nil {
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
			items[i] = project(c, items[i])
		}
		out = RequestPage{Items: items, PageInfo: pageInfo(p, n)}
		return e
	})
	return
}
func (s *Service) UpdateRequest(ctx context.Context, c Caller, in UpdateRequest) (out AdditionRequest, err error) {
	if err = authenticate(c); err != nil {
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
		if !visible(c, r) {
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
		p := r.Proposal
		q := in.Proposal
		if m["name"] {
			p.Name = q.Name
		}
		if m["is_supplier"] {
			p.IsSupplier = q.IsSupplier
		}
		if m["category_ids"] {
			p.CategoryIDs = q.CategoryIDs
		}
		if m["building_id"] {
			p.BuildingID = q.BuildingID
		}
		if m["floor"] {
			p.Floor = q.Floor
		}
		if m["coordinates"] {
			p.CoordinatesMissing = q.CoordinatesMissing
			p.Latitude = q.Latitude
			p.Longitude = q.Longitude
		}
		if m["open_from"] {
			p.OpenFrom = q.OpenFrom
			p.OpenTo = q.OpenTo
		}
		if m["contact"] {
			p.Contact = q.Contact
		}
		if m["details"] {
			p.Details = q.Details
		}
		p, e = normalizeProposal(p)
		if e != nil {
			return e
		}
		if e = tx.ValidateReferences(ctx, p); e != nil {
			return e
		}
		r.Proposal = p
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
	if err = authenticate(c); err != nil {
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
		if !visible(c, r) {
			return ErrNotFound
		}
		if target == Withdrawn && r.SubmittedBy != c.ID {
			return ErrPermissionDenied
		}
		if r.Status == target {
			out.Request = project(c, r)
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

// Internal context is retained for server logs; public transports map only
// sentinel errors and never expose dependency failures to callers.
func DependencyError(operation string, err error) error { return fmt.Errorf("%s: %w", operation, err) }
