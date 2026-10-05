package lifecycle

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
	AdminErrNotFound           = shared.ErrNotFound
	AdminErrPermissionDenied   = shared.ErrPermissionDenied
	AdminErrInvalidArgument    = errs.NewBadRequestError("invalid location input")
	AdminErrUnauthenticated    = errs.NewUnauthorizedError("authentication required")
	AdminErrFailedPrecondition = errs.NewFailedPreconditionError("location prerequisites are not met")
	AdminErrAlreadyExists      = errs.NewAlreadyExistsError("idempotency key already used with different input")
	AdminErrAborted            = errs.NewAbortedError("stale location revision")
)

type AdminCreateRequest struct {
	Key   string
	Input Input
}

type AdminUpdateRequest struct {
	ID               string
	ExpectedRevision int64
	Paths            []string
	Input            Input
}

// AdminStore provides a transaction for all reads and writes in an administrative operation.
type AdminStore interface {
	Within(context.Context, func(AdminTx) error) error
}

type AdminTx interface {
	idempotency.Store
	GetForUpdate(context.Context, string) (shared.Location, error)
	ValidateReferences(context.Context, Input) error
	Create(context.Context, Input, time.Time) (shared.Location, error)
	Update(context.Context, string, Input, int64, time.Time) (shared.Location, error)
	SetArchived(context.Context, string, *time.Time, int64, time.Time) (shared.Location, error)
}

type AdminService struct {
	store       AdminStore
	clock       func() time.Time
	idempotency *idempotency.Runner
}

func NewAdminService(store AdminStore, clock func() time.Time) *AdminService {
	if clock == nil {
		clock = time.Now
	}
	return &AdminService{store: store, clock: clock, idempotency: idempotency.New(clock)}
}

func requireLocationAdmin(ctx context.Context) error {
	c, ok := auth.CallerFromContext(ctx)
	if !ok || strings.TrimSpace(c.ID) == "" {
		return AdminErrUnauthenticated
	}
	if !c.Admin {
		return AdminErrPermissionDenied
	}
	return nil
}

func (s *AdminService) Create(ctx context.Context, req AdminCreateRequest) (out shared.Location, err error) {
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
	err = s.store.Within(ctx, func(tx AdminTx) error {
		id, e := s.idempotency.Run(ctx, tx, idempotency.Scope{Caller: caller.ID, Method: "CreateLocation", Key: key}, hash, func(now time.Time) (string, error) {
			if e := tx.ValidateReferences(ctx, in); e != nil {
				return "", e
			}
			created, e := tx.Create(ctx, in, now)
			return created.ID, e
		})
		if errors.Is(e, idempotency.ErrConflict) {
			return AdminErrAlreadyExists
		}
		if errors.Is(e, idempotency.ErrInvalidKey) {
			return AdminErrInvalidArgument
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

func (s *AdminService) Update(ctx context.Context, req AdminUpdateRequest) (out shared.Location, err error) {
	if err = requireLocationAdmin(ctx); err != nil {
		return out, err
	}
	id, err := canonicalID(req.ID)
	if err != nil {
		return out, err
	}
	if req.ExpectedRevision <= 0 {
		return out, AdminErrInvalidArgument
	}
	mask, err := validateMask(req.Paths)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx AdminTx) error {
		current, e := tx.GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if current.Revision != req.ExpectedRevision {
			return AdminErrAborted
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

func (s *AdminService) Archive(ctx context.Context, id string) (shared.Location, error) {
	return s.setArchived(ctx, id, true)
}

func (s *AdminService) Unarchive(ctx context.Context, id string) (shared.Location, error) {
	return s.setArchived(ctx, id, false)
}

func (s *AdminService) setArchived(ctx context.Context, id string, archive bool) (out shared.Location, err error) {
	if err = requireLocationAdmin(ctx); err != nil {
		return out, err
	}
	id, err = canonicalID(id)
	if err != nil {
		return out, err
	}
	err = s.store.Within(ctx, func(tx AdminTx) error {
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
