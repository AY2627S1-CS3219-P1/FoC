package location

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/google/uuid"
)

var (
	ErrInvalidArgument    = errs.NewBadRequestError("invalid location input")
	ErrUnauthenticated    = errs.NewUnauthorizedError("authentication required")
	ErrFailedPrecondition = errs.NewFailedPreconditionError("location prerequisites are not met")
	ErrAlreadyExists      = errs.NewAlreadyExistsError("idempotency key already used with different input")
	ErrAborted            = errs.NewAbortedError("stale location revision")
)

// Input contains exactly the fields an administrator may write.
type Input struct {
	Name        string
	IsSupplier  bool
	CategoryIDs []string
	BuildingID  string
	Floor       *string
	Coordinates *Coordinates
	OpensAt     *Clock
	ClosesAt    *Clock
	Contact     *string
	Details     string
}

type CreateRequest struct {
	Key   string
	Input Input
}

type UpdateRequest struct {
	ID               string
	ExpectedRevision int64
	Paths            []string
	Input            Input
}

// MutationStore provides a transaction for all reads and writes in a mutation.
type MutationStore interface {
	Within(context.Context, func(MutationTx) error) error
}

type MutationTx interface {
	idempotency.Store
	GetForUpdate(context.Context, string) (Location, error)
	ValidateReferences(context.Context, Input) error
	Create(context.Context, Input, time.Time) (Location, error)
	Update(context.Context, string, Input, int64, time.Time) (Location, error)
	SetArchived(context.Context, string, *time.Time, int64, time.Time) (Location, error)
}

type MutationService struct {
	store       MutationStore
	clock       func() time.Time
	idempotency *idempotency.Runner
}

func NewMutationService(store MutationStore, clock func() time.Time) *MutationService {
	if clock == nil {
		clock = time.Now
	}
	return &MutationService{store: store, clock: clock, idempotency: idempotency.New(clock)}
}

func requireAdmin(c Caller) error {
	if strings.TrimSpace(c.ID) == "" {
		return ErrUnauthenticated
	}
	if !c.Admin {
		return ErrPermissionDenied
	}
	return nil
}

func (s *MutationService) Create(ctx context.Context, caller Caller, req CreateRequest) (out Location, err error) {
	if err = requireAdmin(caller); err != nil {
		return out, err
	}
	key, err := canonicalID(req.Key)
	if err != nil {
		return out, err
	}
	in, err := normalizeInput(req.Input)
	if err != nil {
		return out, err
	}
	// Category order does not change the Location. Hash the sorted canonical IDs.
	hashInput := in
	hashInput.CategoryIDs = slices.Clone(in.CategoryIDs)
	slices.Sort(hashInput.CategoryIDs)
	hash, err := idempotency.Hash(hashInput)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx MutationTx) error {
		id, e := s.idempotency.Run(ctx, tx, idempotency.Scope{Caller: caller.ID, Method: "CreateLocation", Key: key}, hash, func(now time.Time) (string, error) {
			if e := tx.ValidateReferences(ctx, in); e != nil {
				return "", e
			}
			created, e := tx.Create(ctx, in, now)
			return created.ID, e
		})
		if errors.Is(e, idempotency.ErrConflict) {
			return ErrAlreadyExists
		}
		if errors.Is(e, idempotency.ErrInvalidKey) {
			return ErrInvalidArgument
		}
		if e != nil {
			return e
		}
		out, e = tx.GetForUpdate(ctx, id)
		return e
	})
	if err != nil {
		return Location{}, err
	}
	return out, nil
}

func (s *MutationService) Update(ctx context.Context, caller Caller, req UpdateRequest) (out Location, err error) {
	if err = requireAdmin(caller); err != nil {
		return out, err
	}
	id, err := canonicalID(req.ID)
	if err != nil {
		return out, err
	}
	if req.ExpectedRevision <= 0 {
		return out, ErrInvalidArgument
	}
	mask, err := validateMask(req.Paths)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx MutationTx) error {
		current, e := tx.GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if current.Revision != req.ExpectedRevision {
			return ErrAborted
		}
		merged := mergeInput(current, req.Input, mask)
		in, e := normalizeInput(merged)
		if e != nil {
			return e
		}
		if e = tx.ValidateReferences(ctx, in); e != nil {
			return e
		}
		out, e = tx.Update(ctx, id, in, req.ExpectedRevision, s.clock().UTC())
		return e
	})
	if err != nil {
		return Location{}, err
	}
	return out, nil
}

func (s *MutationService) Archive(ctx context.Context, caller Caller, id string) (Location, error) {
	return s.setArchived(ctx, caller, id, true)
}

func (s *MutationService) Unarchive(ctx context.Context, caller Caller, id string) (Location, error) {
	return s.setArchived(ctx, caller, id, false)
}

func (s *MutationService) setArchived(ctx context.Context, caller Caller, id string, archive bool) (out Location, err error) {
	if err = requireAdmin(caller); err != nil {
		return out, err
	}
	id, err = canonicalID(id)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx MutationTx) error {
		current, e := tx.GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if (current.ArchivedAt != nil) == archive {
			out = current
			return nil
		}
		now := s.clock().UTC()
		var at *time.Time
		if archive {
			at = &now
		}
		out, e = tx.SetArchived(ctx, id, at, current.Revision, now)
		return e
	})
	if err != nil {
		return Location{}, err
	}
	return out, nil
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

func validCoordinates(c Coordinates) bool {
	return !math.IsNaN(c.Latitude) && !math.IsInf(c.Latitude, 0) && c.Latitude >= -90 && c.Latitude <= 90 &&
		!math.IsNaN(c.Longitude) && !math.IsInf(c.Longitude, 0) && c.Longitude >= -180 && c.Longitude <= 180
}

func validClock(c Clock) bool {
	return c.Hour >= 0 && c.Hour < 24 && c.Minute >= 0 && c.Minute < 60
}

var writablePaths = map[string]bool{
	"name": true, "is_supplier": true, "category_ids": true, "building_id": true,
	"floor": true, "coordinates": true, "opens_at": true, "closes_at": true,
	"contact": true, "details": true,
}

func validateMask(paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}
	mask := make(map[string]bool, len(paths))
	for _, path := range paths {
		if !writablePaths[path] || mask[path] {
			return nil, ErrInvalidArgument
		}
		mask[path] = true
	}
	if mask["opens_at"] != mask["closes_at"] {
		return nil, ErrInvalidArgument
	}
	return mask, nil
}

func mergeInput(current Location, patch Input, mask map[string]bool) Input {
	categoryIDs := make([]string, len(current.Categories))
	for i, category := range current.Categories {
		categoryIDs[i] = category.ID
	}
	coordinates := current.Coordinates
	in := Input{
		Name: current.Name, IsSupplier: current.IsSupplier, CategoryIDs: categoryIDs,
		BuildingID: current.Building.ID, Floor: current.Floor, Coordinates: &coordinates,
		OpensAt: current.OpensAt, ClosesAt: current.ClosesAt, Contact: current.Contact,
		Details: current.Details,
	}
	if mask["name"] {
		in.Name = patch.Name
	}
	if mask["is_supplier"] {
		in.IsSupplier = patch.IsSupplier
	}
	if mask["category_ids"] {
		in.CategoryIDs = slices.Clone(patch.CategoryIDs)
	}
	if mask["building_id"] {
		in.BuildingID = patch.BuildingID
	}
	if mask["floor"] {
		in.Floor = patch.Floor
	}
	if mask["coordinates"] {
		in.Coordinates = patch.Coordinates
	}
	if mask["opens_at"] {
		in.OpensAt, in.ClosesAt = patch.OpensAt, patch.ClosesAt
	}
	if mask["contact"] {
		in.Contact = patch.Contact
	}
	if mask["details"] {
		in.Details = patch.Details
	}
	return in
}
