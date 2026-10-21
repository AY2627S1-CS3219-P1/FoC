package order

import (
	"context"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const FenceSuspendedAcceptance Action = "fence_suspended_acceptance"

// AccountSuspensionDecision is the immutable, revision-ordered instruction from
// the owning User Service. Only an authenticated User Service adapter may submit
// one. The API/credential boundary is intentionally separate from this operation.
type AccountSuspensionDecision struct {
	ID, UserID, AdminID, Reason string
	Revision                    int64
	Suspended                   bool
}

type AccountSuspensionTx interface {
	CancellationTx
	AccountDecision(context.Context, string) (*AccountSuspensionDecision, error)
	SaveAccountDecision(context.Context, AccountSuspensionDecision) error
	AccountOrderIDs(context.Context, string) ([]string, error)
}

type AccountSuspensionRepository interface {
	WithinAccountSuspension(context.Context, func(AccountSuspensionTx) error) error
}

type AccountSuspender struct {
	repository AccountSuspensionRepository
	credit     CancellationCredit
	options    AssignmentOptions
}

func NewAccountSuspender(repository AccountSuspensionRepository, credit CancellationCredit, options AssignmentOptions) *AccountSuspender {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &AccountSuspender{repository: repository, credit: credit, options: options}
}

// Apply atomically installs the authoritative Order fence, captures each current
// pre-pickup assignment for existing recovery, and fences pending Adjust replies.
// The exclusive transaction gate orders this commit against ordinary mutations.
// A delayed suspension or reinstatement cannot overwrite a newer User revision.
func (s *AccountSuspender) Apply(ctx context.Context, decision AccountSuspensionDecision) error {
	for _, id := range []string{decision.ID, decision.UserID, decision.AdminID} {
		if parsed, err := parseID(id); err != nil || parsed.String() != id {
			return ErrInvalid
		}
	}
	if decision.Revision <= 0 || strings.TrimSpace(decision.Reason) == "" || utf8.RuneCountInString(decision.Reason) > 2000 || !utf8.ValidString(decision.Reason) {
		return ErrInvalid
	}

	return s.repository.WithinAccountSuspension(ctx, func(tx AccountSuspensionTx) error {
		// Recognize older/repeated delivery before touching any assignment.
		current, err := tx.AccountDecision(ctx, decision.UserID)
		if err != nil {
			return err
		}
		if current != nil {
			if current.Revision > decision.Revision {
				return ErrConflict
			}
			if current.Revision == decision.Revision {
				if !reflect.DeepEqual(*current, decision) {
					return ErrConflict
				}
				return nil
			}
		}
		if err := tx.SaveAccountDecision(ctx, decision); err != nil {
			return err
		}
		if !decision.Suspended {
			return nil
		}

		// The exclusive gate makes this discovery stable against assignment,
		// pickup and creation commits until all resulting intent is durable.
		ids, err := tx.AccountOrderIDs(ctx, decision.UserID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			o, err := tx.LockOrder(ctx, id)
			if err != nil {
				return err
			}
			offers, err := tx.Relationships(ctx, id)
			if err != nil {
				return err
			}
			if o.Status == AwaitingPickup && o.IsAssignedTo(decision.UserID) && o.PickedUpAt == nil {
				assignment, err := tx.CurrentSuccessfulAssignment(ctx, id)
				if err != nil {
					return err
				}
				if assignment == nil {
					// Preserve the account fence without inventing financial identity.
					// Pickup stays forbidden; this assignment requires reconciliation.
					continue
				}
				bound := NewCanceller(boundSuspensionRecovery{tx}, s.credit, s.options)
				target := SuspensionRemoval{
					DecisionID:   decision.ID,
					OrderID:      id,
					CourierID:    decision.UserID,
					AssignmentID: assignment.ID,
				}
				if _, err := bound.RemoveSuspendedAssignment(ctx, target); err != nil {
					return err
				}
				continue
			}

			// Compensation remains B1's responsibility. Advancing generation
			// here prevents an already-dispatched Adjust reply from assigning.
			if o.Status != AwaitingCourier {
				continue
			}
			active, err := tx.ActiveAssignment(ctx, id)
			if err != nil {
				return err
			}
			if active == nil || (active.Phase != AdjustmentPending && active.Phase != RevertPending) || (active.CourierID != decision.UserID && active.CallerID != decision.UserID) {
				continue
			}
			now := s.options.Now().UTC()
			if active.Phase == AdjustmentPending {
				expected := active.Generation
				active.Generation++
				active.Phase, active.Rejection = RevertPending, "account_suspended"
				active.LeaseUntil, active.NextAttemptAt, active.UpdatedAt = now, now, now
				if err := tx.SaveAssignment(ctx, *active, expected); err != nil {
					return err
				}
			}
			after := cloneOrder(o)
			after.Revision++
			after.UpdatedAt = now
			change, err := NewChange(&o, after, Actor{Kind: System}, FenceSuspendedAcceptance, nil, s.options.NewID)
			if err != nil {
				return err
			}
			// Retire the public offer independently of financial recovery. An
			// insufficient-credit revert must not reopen it after reinstatement.
			for _, offer := range offers {
				if offer.ID == active.OfferID && offer.Status == OfferAccepting {
					offer.Status, offer.UpdatedAt = OfferVoid, now
					change.Offers = append(change.Offers, OfferChange{Offer: offer, ExpectedStatus: OfferAccepting})
				}
			}
			if err := tx.Apply(ctx, change); err != nil {
				return err
			}
		}
		return nil
	})
}

// Bind existing recovery admission to this already-owned exclusive transaction;
// do not open a nested shared-gate transaction or duplicate its financial work.
type boundSuspensionRecovery struct{ CancellationTx }

func (b boundSuspensionRecovery) WithinCancellation(_ context.Context, fn func(CancellationTx) error) error {
	return fn(b.CancellationTx)
}

func (boundSuspensionRecovery) CancellationOrderID(context.Context, string) (string, error) {
	return "", ErrPrecondition
}

func (boundSuspensionRecovery) DueCancellations(context.Context, time.Time, int32) ([]string, error) {
	return nil, ErrPrecondition
}
