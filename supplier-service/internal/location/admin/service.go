package admin

import (
	"context"
	"errors"
	shared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"slices"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
)

var (
	ErrNotFound           = shared.ErrNotFound
	ErrPermissionDenied   = shared.ErrPermissionDenied
	ErrInvalidArgument    = errs.NewBadRequestError("invalid location input")
	ErrUnauthenticated    = errs.NewUnauthorizedError("authentication required")
	ErrFailedPrecondition = errs.NewFailedPreconditionError("location prerequisites are not met")
	ErrAlreadyExists      = errs.NewAlreadyExistsError("idempotency key already used with different input")
	ErrAborted            = errs.NewAbortedError("stale location revision")
)

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

// Store provides a transaction for all reads and writes in an administrative operation.
type Store interface {
	Within(context.Context, func(Tx) error) error
}

type Tx interface {
	idempotency.Store
	GetForUpdate(context.Context, string) (shared.Location, error)
	ValidateReferences(context.Context, Input) error
	Create(context.Context, Input, time.Time) (shared.Location, error)
	Update(context.Context, string, Input, int64, time.Time) (shared.Location, error)
	SetArchived(context.Context, string, *time.Time, int64, time.Time) (shared.Location, error)
}

type Service struct {
	store       Store
	clock       func() time.Time
	idempotency *idempotency.Runner
}

func NewService(store Store, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: store, clock: clock, idempotency: idempotency.New(clock)}
}

func requireLocationAdmin(ctx context.Context) error {
	c, ok := auth.CallerFromContext(ctx)
	if !ok || strings.TrimSpace(c.ID) == "" {
		return ErrUnauthenticated
	}
	if !c.Admin {
		return ErrPermissionDenied
	}
	return nil
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (out shared.Location, err error) {
	if err = requireLocationAdmin(ctx); err != nil {
		return out, err
	}
	caller, _ := auth.CallerFromContext(ctx)
	key, err := canonicalID(req.Key)
	if err != nil {
		return out, err
	}
	in, err := normalizeInput(req.Input)
	if err != nil {
		return out, err
	}
	// shared.Category order does not change the shared.Location. Hash the sorted canonical IDs.
	hashInput := in
	hashInput.CategoryIDs = slices.Clone(in.CategoryIDs)
	slices.Sort(hashInput.CategoryIDs)
	hash, err := idempotency.Hash(hashInput)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx Tx) error {
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
		return shared.Location{}, err
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, req UpdateRequest) (out shared.Location, err error) {
	if err = requireLocationAdmin(ctx); err != nil {
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
	err = s.store.Within(ctx, func(tx Tx) error {
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
		return shared.Location{}, err
	}
	return out, nil
}

func (s *Service) Archive(ctx context.Context, id string) (shared.Location, error) {
	return s.setArchived(ctx, id, true)
}

func (s *Service) Unarchive(ctx context.Context, id string) (shared.Location, error) {
	return s.setArchived(ctx, id, false)
}

func (s *Service) setArchived(ctx context.Context, id string, archive bool) (out shared.Location, err error) {
	if err = requireLocationAdmin(ctx); err != nil {
		return out, err
	}
	id, err = canonicalID(id)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx Tx) error {
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
		return shared.Location{}, err
	}
	return out, nil
}
