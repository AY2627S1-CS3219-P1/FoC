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

type Canceller struct {
	repository CancellationRepository
	credit     CancellationCredit
	now        func() time.Time
	newID      func() string
}

func NewCanceller(repository CancellationRepository, credit CancellationCredit, options AssignmentOptions) *Canceller {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &Canceller{
		repository: repository,
		credit:     credit,
		now:        options.Now,
		newID:      options.NewID,
	}
}

type cancellationRepository struct{ tx CancellationTx }

func (r cancellationRepository) Within(_ context.Context, fn func(Tx) error) error { return fn(r.tx) }

func (s *Canceller) mutate(ctx context.Context, id string, requestKey *IdempotencyKey, fn func(CancellationTx, Order, []Offer) (*Change, error)) error {
	return s.repository.WithinCancellation(ctx, func(tx CancellationTx) error {
		if requestKey != nil {
			if err := tx.LockIdempotencyCaller(ctx, requestKey.Scope); err != nil {
				return err
			}
			// Compare the retained payload before loading the requested Order:
			// changing even to an unknown Order must report key reuse.
			stored, err := tx.FindIdempotencyKey(ctx, requestKey.Scope, requestKey.Key)
			if err != nil {
				return err
			}
			if stored != nil {
				if err := stored.CheckRequestHash(requestKey.RequestHash); err != nil {
					return err
				}
			}
		}
		return NewMutator(cancellationRepository{tx}).Mutate(ctx, id, func(o Order, offers []Offer) (*Change, error) { return fn(tx, o, offers) })
	})
}

func cancellationHash(id, reason string) string {
	data, _ := json.Marshal([]string{id, reason})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Cancel admits intent before any Credit call. Suspension does not remove participant rights.
func (s *Canceller) Cancel(ctx context.Context, caller Caller, orderID, reason, key string) (CancellationResult, error) {
	cid, err := parseID(caller.ID)
	if err != nil {
		return CancellationResult{}, ErrForbidden
	}
	caller.ID = cid.String()
	oid, err := parseID(orderID)
	if err != nil {
		return CancellationResult{}, err
	}
	orderID = oid.String()
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 500 {
		return CancellationResult{}, ErrInvalid
	}
	var scope *IdempotencyScope
	if key != "" {
		kid, err := parseID(key)
		if err != nil {
			return CancellationResult{}, err
		}
		key = kid.String()
		scope = &IdempotencyScope{CallerID: caller.ID, Method: CancelOrderMethod}
	}
	hash := cancellationHash(orderID, reason)
	var requestKey *IdempotencyKey
	if scope != nil {
		requestKey = &IdempotencyKey{Scope: *scope, Key: key, RequestHash: hash}
	}
	var operation Cancellation
	err = s.mutate(ctx, orderID, requestKey, func(tx CancellationTx, o Order, offers []Offer) (*Change, error) {
		retain := func() error {
			if scope == nil {
				return nil
			}
			return tx.InsertIdempotencyKey(ctx, acceptanceKey(*scope, key, hash, operation.ID))
		}
		// Resolve retained operations before checking today's participant/lifecycle state.
		if scope != nil {
			stored, err := tx.FindIdempotencyKey(ctx, *scope, key)
			if err != nil {
				return nil, err
			}
			if stored != nil {
				if err := stored.CheckRequestHash(hash); err != nil {
					return nil, err
				}
				operation, err = tx.FindCancellation(ctx, stored.ResourceID)
				if err != nil {
					return nil, ErrIdempotencyIntegrity
				}
				if operation.CallerID != caller.ID || operation.OrderID != orderID || operation.Reason != reason {
					return nil, ErrIdempotencyIntegrity
				}
				return nil, nil
			}
		}
		// Authorize new intent against the locked aggregate, then join pending work.
		if !o.HasParticipant(caller.ID) {
			return nil, ErrForbidden
		}
		now := s.now().UTC()
		if o.PendingCancellationID != nil && o.IsAssignedTo(caller.ID) {
			operation, err = tx.FindCancellation(ctx, *o.PendingCancellationID)
			if err != nil {
				return nil, err
			}
			if operation.Status != CancellationPending || operation.CallerID != caller.ID {
				return nil, ErrIdempotencyIntegrity
			}
			if operation.Reason != reason {
				return nil, ErrConflict
			}
			return nil, retain()
		}
		action := Cancel
		if o.Status == AwaitingPickup && o.IsAssignedTo(caller.ID) {
			action = Withdraw
		}
		next, _, err := Transition(o, action, Actor{Kind: User, ID: caller.ID})
		if err != nil {
			return nil, err
		}
		operation = Cancellation{
			ID:            s.newID(),
			OrderID:       o.ID,
			CallerID:      caller.ID,
			Reason:        reason,
			Status:        CancellationSucceeded,
			LeaseUntil:    now,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		after := cloneOrder(o)
		after.Revision++
		after.UpdatedAt = now
		if action == Withdraw {
			operation.Status, operation.CourierID, operation.AssignedAt = CancellationPending, caller.ID, clonePointer(o.AssignedAt)
			assignment, err := tx.CurrentSuccessfulAssignment(ctx, o.ID)
			if err != nil {
				return nil, err
			}
			if assignment != nil {
				operation.AssignmentID, operation.OriginalAmount = assignment.ID, assignment.OriginalAmount
			}
			after.PendingCancellationID = &operation.ID
			action = BeginWithdrawal
		} else {
			after.Status, after.ClosedAt = next, &now
			after.CloseReason = nil
			if reason != "" {
				after.CloseReason = &reason
			}
			if o.PendingCancellationID != nil {
				pending, err := tx.FindCancellation(ctx, *o.PendingCancellationID)
				if err != nil {
					return nil, err
				}
				pending.Status, pending.UpdatedAt = CancellationSuperseded, now
				pending.Generation++
				if err := tx.SaveCancellation(ctx, pending, pending.Generation-1); err != nil {
					return nil, err
				}
				after.PendingCancellationID = nil
			}
			// Fence the in-flight Adjust and persist compensation before closing.
			active, err := tx.ActiveAssignment(ctx, o.ID)
			if err != nil {
				return nil, err
			}
			if active != nil {
				expected := active.Generation
				active.Generation++
				active.Phase, active.Rejection = RevertPending, "order_unavailable"
				active.LeaseUntil, active.NextAttemptAt, active.UpdatedAt = now, now, now
				if err := tx.SaveAssignment(ctx, *active, expected); err != nil {
					return nil, err
				}
			}
		}
		if err := tx.InsertCancellation(ctx, operation); err != nil {
			return nil, err
		}
		if err := retain(); err != nil {
			return nil, err
		}
		details, _ := json.Marshal(map[string]string{"operation_id": operation.ID, "reason": reason})
		change, err := NewChange(&o, after, Actor{Kind: User, ID: caller.ID}, action, details, s.newID)
		if err != nil {
			return nil, err
		}
		if action == Cancel {
			for _, f := range offers {
				if f.Status == OfferOpen || f.Status == OfferAccepting {
					before := f.Status
					f.Status, f.UpdatedAt = OfferVoid, now
					change.Offers = append(change.Offers, OfferChange{Offer: f, ExpectedStatus: before})
				}
			}
		}
		return &change, nil
	})
	if err != nil {
		return CancellationResult{}, err
	}
	// Admission has committed. Remote reversal and current-result loading are separate.
	if operation.Status == CancellationPending && s.credit != nil {
		inline, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.ProcessCancellation(inline, operation.ID)
		cancel()
		if ctx.Err() != nil {
			return CancellationResult{}, ctx.Err()
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return CancellationResult{}, ErrUnavailable
		}
	}
	var result CancellationResult
	err = s.mutate(ctx, orderID, nil, func(tx CancellationTx, o Order, _ []Offer) (*Change, error) {
		current, err := tx.FindCancellation(ctx, operation.ID)
		if err != nil {
			return nil, err
		}
		result = CancellationResult{Order: o, OperationID: current.ID, Status: current.Status}
		return nil, nil
	})
	return result, err
}
