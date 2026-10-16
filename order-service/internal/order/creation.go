package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Creator struct {
	repository CreationRepository
	locations  CreationLocations
	credit     CreationCredit
	enabled    bool
	now        func() time.Time
	newID      func() string
}

func NewCreator(repository CreationRepository, locations CreationLocations, credit CreationCredit, options CreationOptions) *Creator {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &Creator{
		repository: repository, locations: locations, credit: credit,
		enabled: options.AdmissionsEnabled, now: options.Now, newID: options.NewID,
	}
}

// Create first recognizes retained operations. Mutable deadlines, locations and
// admission configuration cannot invalidate an already admitted identical retry.
func (s *Creator) Create(ctx context.Context, caller Caller, input CreateInput) (Order, error) {
	if caller.ID == "" || caller.Suspended {
		return Order{}, ErrForbidden
	}
	uid, err := parseID(caller.ID)
	if err != nil {
		return Order{}, ErrForbidden
	}
	caller.ID = uid.String()

	input, canonical, hash, err := canonicalCreation(input)
	if err != nil {
		return Order{}, err
	}

	workflow, err := s.findCreation(ctx, caller.ID, input.Key)
	if err != nil {
		return Order{}, err
	}
	if workflow != nil {
		return s.resume(ctx, *workflow, hash)
	}
	if len(input.ImageIDs) != 0 {
		return Order{}, ErrImagesUnavailable
	}
	if !s.enabled || s.locations == nil || s.credit == nil {
		return Order{}, ErrUnavailable
	}
	if !input.ExpiresAt.After(s.now()) {
		return s.recheckCreation(ctx, caller.ID, input.Key, hash, ErrInvalid)
	}

	// Snapshot remote references before opening the admission transaction. Recovery
	// uses these committed snapshots and never stores or needs the user's token.
	pickup, err := s.locations.GetLocation(ctx, input.PickupID)
	if err != nil {
		return s.recheckCreation(ctx, caller.ID, input.Key, hash, err)
	}
	delivery, err := s.locations.GetLocation(ctx, input.DeliveryID)
	if err != nil {
		return s.recheckCreation(ctx, caller.ID, input.Key, hash, err)
	}
	if pickup.Archived || delivery.Archived {
		return s.recheckCreation(ctx, caller.ID, input.Key, hash, ErrPrecondition)
	}
	if pickup.Snapshot.LocationID != input.PickupID || delivery.Snapshot.LocationID != input.DeliveryID {
		return Order{}, ErrUnavailable
	}

	err = s.repository.WithinCreation(ctx, func(tx CreationTx) error {
		if err := tx.LockCreationCaller(ctx, caller.ID); err != nil {
			return err
		}
		now := s.now().UTC()
		// Another request may have admitted this key during the remote lookups.
		existing, err := tx.FindCreationKey(ctx, caller.ID, input.Key, now)
		if err != nil {
			return err
		}
		if existing != nil {
			workflow = existing
			return nil
		}
		if !input.ExpiresAt.After(now) {
			return ErrInvalid
		}
		count, err := tx.CountRecentCreations(ctx, caller.ID, now.Add(-time.Minute))
		if err != nil {
			return err
		}
		if count >= 10 {
			return ErrRateLimited
		}

		order := Order{
			ID: s.newID(), RequesterID: caller.ID, Status: PendingReservation,
			Pickup: pickup.Snapshot, Delivery: delivery.Snapshot, DeliveryInstructions: input.Instructions,
			ItemDescription: input.ItemDescription, RewardCredits: input.RewardCredits,
			DeliveryDuration: input.DeliveryDuration, ExpiresAt: input.ExpiresAt,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		change, err := NewChange(nil, order, Actor{Kind: User, ID: caller.ID}, Create, nil, s.newID)
		if err != nil {
			return err
		}
		if err := tx.Apply(ctx, change); err != nil {
			return err
		}
		w := CreationWorkflow{
			ID: s.newID(), OrderID: order.ID, RequesterID: caller.ID, Canonical: canonical, Hash: hash,
			Amount: input.RewardCredits, ExpiresAt: input.ExpiresAt, Phase: ReservePending,
			LeaseUntil: now, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.InsertCreation(ctx, w, input.Key); err != nil {
			return err
		}
		workflow = &w
		return nil
	})
	if err != nil {
		return Order{}, err
	}
	return s.resume(ctx, *workflow, hash)
}

func canonicalCreation(in CreateInput) (CreateInput, []byte, string, error) {
	for _, field := range []*string{&in.Key, &in.PickupID, &in.DeliveryID} {
		id, err := parseID(*field)
		if err != nil {
			return in, nil, "", ErrInvalid
		}
		*field = id.String()
	}
	if len(in.ImageIDs) > 5 {
		return in, nil, "", ErrInvalid
	}
	images := append([]string(nil), in.ImageIDs...)
	seen := map[string]bool{}
	for i, value := range images {
		id, err := parseID(value)
		if err != nil || seen[id.String()] {
			return in, nil, "", ErrInvalid
		}
		images[i] = id.String()
		seen[images[i]] = true
	}
	in.ImageIDs = images
	in.ItemDescription = strings.TrimSpace(in.ItemDescription)
	if in.Instructions != nil {
		value := strings.TrimSpace(*in.Instructions)
		in.Instructions = &value
	}
	if in.PickupID == in.DeliveryID || in.RewardCredits <= 0 {
		return in, nil, "", ErrInvalid
	}
	descriptionLength := utf8.RuneCountInString(in.ItemDescription)
	if descriptionLength < 1 || descriptionLength > 1000 || (in.Instructions != nil && utf8.RuneCountInString(*in.Instructions) > 500) {
		return in, nil, "", ErrInvalid
	}
	if in.DeliveryDuration < 5*time.Minute || in.DeliveryDuration > 24*time.Hour || in.ExpiresAt.IsZero() || in.ExpiresAt.Year() < 1 || in.ExpiresAt.Year() > 9999 {
		return in, nil, "", ErrInvalid
	}
	in.ExpiresAt = in.ExpiresAt.UTC()
	canonical, err := json.Marshal(in)
	if err != nil {
		return in, nil, "", err
	}
	digest := sha256.Sum256(canonical)
	return in, canonical, hex.EncodeToString(digest[:]), nil
}

func (s *Creator) resume(ctx context.Context, w CreationWorkflow, hash string) (Order, error) {
	if w.Hash != hash {
		return Order{}, ErrAlreadyExists
	}
	if !w.IsResolved() && s.credit != nil {
		inline, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.ProcessCreation(inline, w.OrderID)
		cancel()
		if ctx.Err() != nil {
			return Order{}, ctx.Err()
		}
		// An inline budget/dependency failure leaves durable work for recovery. A
		// database failure is still reported; the admitted identity remains intact.
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return Order{}, err
		}
	}
	var result Order
	err := s.repository.WithinCreation(ctx, func(tx CreationTx) error {
		current, err := tx.LockOrder(ctx, w.OrderID)
		if err != nil {
			return err
		}
		workflow, err := tx.LockCreation(ctx, w.OrderID)
		if err != nil {
			return err
		}
		switch workflow.Phase {
		case CreationRejected:
			return workflow.Rejection
		case CreationOpened:
			if workflow.Result == nil {
				return ErrConflict
			}
			result = cloneOrder(*workflow.Result)
		default:
			result = current
		}
		return nil
	})
	return result, err
}

func (s *Creator) findCreation(ctx context.Context, caller, key string) (*CreationWorkflow, error) {
	var result *CreationWorkflow
	err := s.repository.WithinCreation(ctx, func(tx CreationTx) error {
		if err := tx.LockCreationCaller(ctx, caller); err != nil {
			return err
		}
		var err error
		result, err = tx.FindCreationKey(ctx, caller, key, s.now())
		return err
	})
	return result, err
}

// A concurrent identical request can be admitted while Supplier preflight is in
// flight. Recognize it before returning a mutable preflight/deadline failure.
func (s *Creator) recheckCreation(ctx context.Context, caller, key, hash string, cause error) (Order, error) {
	existing, err := s.findCreation(ctx, caller, key)
	if err != nil {
		return Order{}, err
	}
	if existing != nil {
		return s.resume(ctx, *existing, hash)
	}
	return Order{}, cause
}
