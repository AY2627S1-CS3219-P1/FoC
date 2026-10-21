package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Assigner struct {
	repository AssignmentRepository
	credit     AssignmentCredit
	now        func() time.Time
	newID      func() string
}

func NewAssigner(repository AssignmentRepository, credit AssignmentCredit, options AssignmentOptions) *Assigner {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &Assigner{repository: repository, credit: credit, now: options.Now, newID: options.NewID}
}

// assignmentRepository binds Mutator to an already-owned assignment transaction.
// Workflow and aggregate writes therefore share one commit and rollback boundary.
type assignmentRepository struct{ tx AssignmentTx }

func (r assignmentRepository) Within(_ context.Context, fn func(Tx) error) error {
	return fn(r.tx)
}

func (s *Assigner) mutate(ctx context.Context, orderID string, scope *IdempotencyScope, build func(AssignmentTx, Order, []Offer) (*Change, error)) error {
	return s.repository.WithinAssignment(ctx, func(tx AssignmentTx) error {
		if scope != nil {
			if err := tx.LockIdempotencyCaller(ctx, *scope); err != nil {
				return err
			}
		}
		return NewMutator(assignmentRepository{tx}).Mutate(ctx, orderID, func(o Order, offers []Offer) (*Change, error) {
			change, err := build(tx, o, offers)
			if err != nil || change == nil {
				return change, err
			}
			var attempt *AssignmentAttempt
			if change.Action != ProposeOffer {
				for _, update := range change.Offers {
					if change.Action == ResolveAssignment || update.ExpectedStatus == OfferAccepting || update.Offer.Status == OfferAccepting {
						attempt, err = tx.OfferAssignment(ctx, update.Offer.ID)
						if err != nil {
							return nil, err
						}
						break
					}
				}
			}
			if err := validateAssignmentOffers(o, offers, *change, attempt); err != nil {
				return nil, err
			}
			return change, nil
		})
	})
}

func assignmentCaller(c Caller) (Caller, error) {
	id, err := parseID(c.ID)
	if err != nil || c.Suspended {
		return Caller{}, ErrForbidden
	}
	c.ID = id.String()
	return c, nil
}

func assignmentHash(orderID string, amount int64) string {
	payload, _ := json.Marshal(struct {
		OrderID string
		Amount  int64
	}{orderID, amount})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// MakeOffer records a proposal, without asking Credit about the requester's
// balance. Replacing a proposal and retaining its retry key commit atomically.
func (s *Assigner) MakeOffer(ctx context.Context, caller Caller, orderID string, credits int64, key string) (Offer, error) {
	caller, err := assignmentCaller(caller)
	if err != nil {
		return Offer{}, err
	}
	id, err := parseID(orderID)
	if err != nil {
		return Offer{}, err
	}
	kid, err := parseID(key)
	if err != nil || credits <= 0 {
		return Offer{}, ErrInvalid
	}
	orderID, key = id.String(), kid.String()
	scope := IdempotencyScope{CallerID: caller.ID, Method: MakeOfferMethod}
	hash := assignmentHash(orderID, credits)
	var result Offer
	err = s.mutate(ctx, orderID, &scope, func(tx AssignmentTx, o Order, offers []Offer) (*Change, error) {
		if o.IsRequestedBy(caller.ID) {
			return nil, ErrForbidden
		}
		now := s.now().UTC()
		stored, err := tx.FindIdempotencyKey(ctx, scope, key)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			if err := stored.CheckRequestHash(hash); err != nil {
				return nil, err
			}
			found := false
			for _, f := range offers {
				if f.ID == stored.ResourceID && f.CourierID == caller.ID {
					result, found = f, true
				}
			}
			if !found {
				return nil, ErrIdempotencyIntegrity
			}
			if stored.ExpiresAt.After(now) || result.Status == OfferAccepting {
				return nil, nil
			}
			if err := tx.DeleteIdempotencyKey(ctx, scope, key); err != nil {
				return nil, err
			}
		}
		if err := s.canPropose(ctx, tx, o, now); err != nil {
			return nil, err
		}
		if credits <= o.RewardCredits {
			return nil, ErrInvalid
		}
		result = Offer{ID: s.newID(), OrderID: orderID, CourierID: caller.ID, Credits: credits, Status: OfferOpen, CreatedAt: now, UpdatedAt: now}
		change, err := s.metadata(o, caller, ProposeOffer, now)
		if err != nil {
			return nil, err
		}
		for _, f := range offers {
			if f.CourierID == caller.ID && f.Status == OfferOpen {
				old := f
				f.Status, f.UpdatedAt = OfferVoid, now
				change.Offers = append(change.Offers, OfferChange{Offer: f, ExpectedStatus: old.Status})
			}
		}
		change.Offers = append(change.Offers, OfferChange{Offer: result})
		if err := tx.InsertIdempotencyKey(ctx, IdempotencyKey{Scope: scope, Key: key, ResourceID: result.ID, RequestHash: hash, ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
			return nil, err
		}
		return change, nil
	})
	return result, err
}

func (s *Assigner) canPropose(ctx context.Context, tx AssignmentTx, o Order, now time.Time) error {
	if o.Status != AwaitingCourier || !o.ExpiresAt.After(now) {
		return ErrPrecondition
	}
	active, err := tx.ActiveAssignment(ctx, o.ID)
	if err != nil {
		return err
	}
	if active != nil {
		return ErrConflict
	}
	return nil
}

func (s *Assigner) metadata(o Order, caller Caller, action Action, now time.Time) (*Change, error) {
	after := cloneOrder(o)
	after.Revision++
	after.UpdatedAt = now
	change, err := NewChange(&o, after, Actor{Kind: User, ID: caller.ID}, action, nil, s.newID)
	return &change, err
}

func (s *Assigner) AcceptOrder(ctx context.Context, caller Caller, orderID, key string) (AssignmentResult, error) {
	caller, err := assignmentCaller(caller)
	if err != nil {
		return AssignmentResult{}, err
	}
	id, err := parseID(orderID)
	if err != nil {
		return AssignmentResult{}, err
	}
	kid, err := parseID(key)
	if err != nil {
		return AssignmentResult{}, err
	}
	orderID, key = id.String(), kid.String()
	scope := IdempotencyScope{CallerID: caller.ID, Method: AcceptOrderMethod}
	hash := assignmentHash(orderID, 0)
	var attempt AssignmentAttempt
	err = s.mutate(ctx, orderID, &scope, func(tx AssignmentTx, o Order, offers []Offer) (*Change, error) {
		if o.IsRequestedBy(caller.ID) {
			return nil, ErrForbidden
		}
		stored, err := tx.FindIdempotencyKey(ctx, scope, key)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			if err := stored.CheckRequestHash(hash); err != nil {
				return nil, err
			}
			attempt, err = tx.FindAssignment(ctx, stored.ResourceID)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrIdempotencyIntegrity
			}
			if err == nil && (attempt.CallerID != caller.ID || attempt.Method != AcceptOrderMethod || attempt.Key != key || attempt.RequestHash != hash || attempt.OrderID != orderID) {
				return nil, ErrIdempotencyIntegrity
			}
			return nil, err
		}
		// Resolve retained keys before checking public admission.
		now := s.now().UTC()
		if o.Status != AwaitingCourier || !o.ExpiresAt.After(now) || o.ReservedCredits <= 0 {
			return nil, ErrNotFound
		}
		if s.credit == nil {
			return nil, ErrUnavailable
		}
		if cause := s.canPropose(ctx, tx, o, now); cause != nil {
			if !errors.Is(cause, ErrPrecondition) && !errors.Is(cause, ErrConflict) {
				return nil, cause
			}
			// Retain at most one rejected contention identity per courier and Order.
			rejected, err := tx.HasRejectedAcceptance(ctx, orderID, caller.ID)
			if err != nil {
				return nil, err
			}
			if rejected {
				return nil, ErrPrecondition
			}
			// A rejected attempt also needs a permanent identity. Returning a
			// business error here would roll back that identity and let an old
			// retry become a new assignment after the request reopens.
			attempt = AssignmentAttempt{
				ID: s.newID(), OrderID: orderID, CallerID: caller.ID, CourierID: caller.ID,
				Method: AcceptOrderMethod, Key: key, RequestHash: hash, Amount: o.RewardCredits,
				OriginalAmount: o.ReservedCredits, Phase: AssignmentRejected,
				Rejection: "order_unavailable", CreatedAt: now, UpdatedAt: now,
				LeaseUntil: now, NextAttemptAt: now,
			}
			if errors.Is(cause, ErrConflict) {
				attempt.Rejection = "assignment_in_progress"
			}
			if err := tx.InsertAssignment(ctx, attempt); err != nil {
				return nil, err
			}
			return nil, tx.InsertIdempotencyKey(ctx, acceptanceKey(scope, key, hash, attempt.ID))
		}
		// Every assignment, including the original reward, has an immutable
		// offer identity for Credit. Existing public counteroffers remain separate.
		f := Offer{ID: s.newID(), OrderID: orderID, CourierID: caller.ID, Credits: o.RewardCredits, Status: OfferAccepting, CreatedAt: now, UpdatedAt: now}
		attempt = s.attempt(o, f, caller, AcceptOrderMethod, key, hash, now)
		if err := tx.InsertAssignment(ctx, attempt); err != nil {
			return nil, err
		}
		// AcceptOrder mappings never expire while their Order is retained.
		if err := tx.InsertIdempotencyKey(ctx, acceptanceKey(scope, key, hash, attempt.ID)); err != nil {
			return nil, err
		}
		change, err := s.metadata(o, caller, BeginAssignment, now)
		if err == nil {
			change.Offers = []OfferChange{{Offer: f}}
		}
		return change, err
	})
	if err != nil {
		return AssignmentResult{}, err
	}
	return s.resumeAssignment(ctx, caller, attempt)
}

func (s *Assigner) AcceptOffer(ctx context.Context, caller Caller, offerID string) (AssignmentResult, error) {
	caller, err := assignmentCaller(caller)
	if err != nil {
		return AssignmentResult{}, err
	}
	id, err := parseID(offerID)
	if err != nil {
		return AssignmentResult{}, err
	}
	offerID = id.String()
	orderID, err := s.repository.OfferOrderID(ctx, offerID)
	if err != nil {
		return AssignmentResult{}, err
	}
	var attempt AssignmentAttempt
	err = s.mutate(ctx, orderID, nil, func(tx AssignmentTx, o Order, offers []Offer) (*Change, error) {
		if !o.IsRequestedBy(caller.ID) {
			return nil, ErrNotFound
		}
		previous, err := tx.OfferAssignment(ctx, offerID)
		if err != nil {
			return nil, err
		}
		if previous != nil && (previous.Phase != AssignmentRejected || previous.Rejection != "insufficient_credits") {
			attempt = *previous
			return nil, nil
		}
		var selected *Offer
		for _, f := range offers {
			if f.ID == offerID {
				copy := f
				selected = &copy
			}
		}
		if selected == nil {
			return nil, ErrNotFound
		}
		if s.credit == nil {
			return nil, ErrUnavailable
		}
		now := s.now().UTC()
		if err := s.canPropose(ctx, tx, o, now); err != nil {
			return nil, err
		}
		if selected.Status != OfferOpen || selected.CourierID == o.RequesterID || selected.Credits <= o.RewardCredits {
			return nil, ErrPrecondition
		}
		attempt = s.attempt(o, *selected, caller, "/order.v1.OrderService/AcceptOffer", "", "", now)
		if err := tx.InsertAssignment(ctx, attempt); err != nil {
			return nil, err
		}
		change, err := s.metadata(o, caller, BeginAssignment, now)
		if err == nil {
			selected.Status, selected.UpdatedAt = OfferAccepting, now
			change.Offers = []OfferChange{{Offer: *selected, ExpectedStatus: OfferOpen}}
		}
		return change, err
	})
	if err != nil {
		return AssignmentResult{}, err
	}
	return s.resumeAssignment(ctx, caller, attempt)
}

func (s *Assigner) attempt(o Order, f Offer, caller Caller, method, key, hash string, now time.Time) AssignmentAttempt {
	return AssignmentAttempt{
		ID: s.newID(), OrderID: o.ID, OfferID: f.ID, CallerID: caller.ID, CourierID: f.CourierID,
		Method: method, Key: key, RequestHash: hash, Amount: f.Credits, OriginalAmount: o.ReservedCredits,
		Phase: AdjustmentPending, LeaseUntil: now, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *Assigner) resumeAssignment(ctx context.Context, caller Caller, a AssignmentAttempt) (AssignmentResult, error) {
	if !a.IsResolved() && s.credit != nil {
		inline, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.ProcessAssignment(inline, a.ID)
		cancel()
		if ctx.Err() != nil {
			return AssignmentResult{}, ctx.Err()
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			// Admission already committed durable intent. A processing failure
			// does not establish financial rejection, even if an internal
			// validation/CAS error would otherwise map to a business RPC code.
			return AssignmentResult{}, ErrUnavailable
		}
	}
	var result AssignmentResult
	err := s.mutate(ctx, a.OrderID, nil, func(tx AssignmentTx, o Order, offers []Offer) (*Change, error) {
		current, err := tx.FindAssignment(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if current.CallerID != caller.ID {
			return nil, ErrNotFound
		}
		result = AssignmentResult{Order: o, AttemptID: current.ID, Phase: current.Phase, Rejection: current.Rejection}
		for _, f := range offers {
			if f.ID == current.OfferID {
				result.Offer = f
			}
		}
		if result.Offer.ID == "" && !(current.Phase == AssignmentRejected && current.OfferID == "") {
			return nil, ErrIdempotencyIntegrity
		}
		return nil, nil
	})
	return result, err
}

func acceptanceKey(scope IdempotencyScope, key, hash, attemptID string) IdempotencyKey {
	return IdempotencyKey{
		Scope: scope, Key: key, ResourceID: attemptID, RequestHash: hash,
		ExpiresAt: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC),
	}
}
