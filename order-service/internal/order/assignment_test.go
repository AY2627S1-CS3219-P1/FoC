package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests derive outcomes from approved acceptance retry decisions: retaining
// attempt identity must prevent delayed replay from assigning a courier again.
type replayAssignmentRepository struct {
	order      Order
	offers     []Offer
	attempt    AssignmentAttempt
	key        *IdempotencyKey
	writes     int
	keysLocked bool
}

func (r *replayAssignmentRepository) WithinAssignment(_ context.Context, fn func(AssignmentTx) error) error {
	return fn(r)
}
func (r *replayAssignmentRepository) AssignmentOrderID(context.Context, string) (string, error) {
	return r.order.ID, nil
}
func (r *replayAssignmentRepository) OfferOrderID(context.Context, string) (string, error) {
	return r.order.ID, nil
}
func (*replayAssignmentRepository) DueAssignments(context.Context, time.Time, int32) ([]string, error) {
	return nil, nil
}
func (r *replayAssignmentRepository) LockOrder(context.Context, string) (Order, error) {
	return cloneOrder(r.order), nil
}
func (r *replayAssignmentRepository) Relationships(context.Context, string) ([]Offer, error) {
	return append([]Offer(nil), r.offers...), nil
}
func (r *replayAssignmentRepository) Apply(context.Context, Change) error {
	r.writes++
	return errors.New("unexpected mutation during replay")
}
func (r *replayAssignmentRepository) LockIdempotencyCaller(context.Context, IdempotencyScope) error {
	r.keysLocked = true
	return nil
}
func (r *replayAssignmentRepository) FindIdempotencyKey(context.Context, IdempotencyScope, string) (*IdempotencyKey, error) {
	return r.key, nil
}
func (r *replayAssignmentRepository) InsertIdempotencyKey(_ context.Context, key IdempotencyKey) error {
	r.writes++
	r.key = &key
	return nil
}
func (r *replayAssignmentRepository) DeleteIdempotencyKey(context.Context, IdempotencyScope, string) error {
	r.writes++
	return nil
}
func (r *replayAssignmentRepository) HasRejectedAcceptance(context.Context, string, string) (bool, error) {
	return false, nil
}
func (r *replayAssignmentRepository) FindAssignment(context.Context, string) (AssignmentAttempt, error) {
	return r.attempt, nil
}
func (*replayAssignmentRepository) ActiveAssignment(context.Context, string) (*AssignmentAttempt, error) {
	return nil, nil
}
func (r *replayAssignmentRepository) OfferAssignment(context.Context, string) (*AssignmentAttempt, error) {
	return &r.attempt, nil
}
func (r *replayAssignmentRepository) InsertAssignment(_ context.Context, attempt AssignmentAttempt) error {
	r.writes++
	r.attempt = attempt
	return nil
}
func (r *replayAssignmentRepository) SaveAssignment(context.Context, AssignmentAttempt, int64) error {
	r.writes++
	return nil
}

func replayAssignmentFixture() (*replayAssignmentRepository, Caller, string) {
	now := time.Now().UTC()
	caller := Caller{ID: uuid.NewString()}
	key := uuid.NewString()
	o := Order{ID: uuid.NewString(), RequesterID: uuid.NewString(), Status: AwaitingCourier, RewardCredits: 20, ReservedCredits: 20, Revision: 8, ExpiresAt: now.Add(time.Hour)}
	f := Offer{ID: uuid.NewString(), OrderID: o.ID, CourierID: caller.ID, Credits: 50, Status: OfferAccepted}
	a := AssignmentAttempt{ID: uuid.NewString(), OrderID: o.ID, OfferID: f.ID, CallerID: caller.ID, CourierID: caller.ID, Method: AcceptOrderMethod, Key: key, RequestHash: assignmentHash(o.ID, 0), Phase: AssignmentSucceeded, Amount: 50, OriginalAmount: 20}
	return &replayAssignmentRepository{order: o, offers: []Offer{f}, attempt: a, key: &IdempotencyKey{Scope: IdempotencyScope{CallerID: caller.ID, Method: AcceptOrderMethod}, Key: key, ResourceID: a.ID, RequestHash: a.RequestHash, ExpiresAt: now.Add(-48 * time.Hour)}}, caller, key
}

func TestAcceptOrderHistoricalReplayDoesNotReassign(t *testing.T) {
	r, caller, key := replayAssignmentFixture()
	// Even a mistakenly short expires_at on retry metadata must not turn an
	// existing assignment resource into a fresh operation.
	result, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOrder(context.Background(), caller, r.order.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != AssignmentSucceeded || result.AttemptID != r.attempt.ID || result.Order.Status != AwaitingCourier || result.Order.CourierID != nil || r.writes != 0 || !r.keysLocked {
		t.Fatalf("replay performed or misrepresented assignment: %+v writes=%d", result, r.writes)
	}
}

func TestAcceptOrderRetainedKeyRejectsDifferentRequest(t *testing.T) {
	r, caller, key := replayAssignmentFixture()
	r.order.ID = uuid.NewString()
	_, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOrder(context.Background(), caller, r.order.ID, key)
	if !errors.Is(err, ErrAlreadyExists) || r.writes != 0 {
		t.Fatalf("changed replay accepted: %v writes=%d", err, r.writes)
	}
}

// Source: R1.2.2 and U6.4 prohibit own-order and suspended-user acceptance.
func TestAcceptanceRejectsOwnOrderAndSuspension(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		r, caller, key := replayAssignmentFixture()
		caller.Suspended = suspended
		if !suspended {
			r.order.RequesterID = caller.ID
		}
		_, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOrder(context.Background(), caller, r.order.ID, key)
		if !errors.Is(err, ErrForbidden) || r.writes != 0 {
			t.Fatalf("forbidden caller accepted: %v", err)
		}
	}
}

// Source: accepted proposal/replay policy. Same key is a retry, never an edit.
func TestMakeOfferRetainedReplayDoesNotReplaceOffer(t *testing.T) {
	r, caller, key := replayAssignmentFixture()
	r.key.Scope.Method = MakeOfferMethod
	r.key.ResourceID = r.offers[0].ID
	r.key.RequestHash = assignmentHash(r.order.ID, 50)
	r.key.ExpiresAt = time.Now().Add(time.Hour)
	result, err := NewAssigner(r, nil, AssignmentOptions{}).MakeOffer(context.Background(), caller, r.order.ID, 50, key)
	if err != nil || result.ID != r.offers[0].ID || result.Status != OfferAccepted || r.writes != 0 {
		t.Fatalf("offer retry changed history: %+v %v", result, err)
	}
	_, err = NewAssigner(r, nil, AssignmentOptions{}).MakeOffer(context.Background(), caller, r.order.ID, 60, key)
	if !errors.Is(err, ErrAlreadyExists) || r.writes != 0 {
		t.Fatalf("changed proposal replay accepted: %v", err)
	}
}

// Source: credit/v1/reservation.proto. A held reservation without acknowledged
// RevertAdjustment does not fence a delayed adjustment request.
func TestRevertRequiresAcknowledgementBeforeHeldRead(t *testing.T) {
	credit := &revertObservationCredit{}
	s := NewAssigner(nil, credit, AssignmentOptions{})
	observation := s.observeAssignment(context.Background(), AssignmentAttempt{ID: uuid.NewString(), OrderID: uuid.NewString(), Phase: RevertPending})
	if observation.reservation != nil || credit.reads != 0 {
		t.Fatal("treated unacknowledged revert as fenced")
	}
}

type revertObservationCredit struct{ reads int }

func (*revertObservationCredit) Adjust(context.Context, AdjustmentIntent) error {
	return ErrUnavailable
}
func (*revertObservationCredit) Revert(context.Context, string, string) error { return ErrUnavailable }
func (c *revertObservationCredit) AssignmentReservation(context.Context, string) (AssignmentReservation, error) {
	c.reads++
	return AssignmentReservation{State: "held", OriginalAmount: 20, ReservedAmount: 20}, nil
}

// Source: the approved lifetime attempt-identity rule applies to rejected
// attempts too. A losing courier's delayed retry must not become a new win.
func TestRejectedAcceptanceRemainsRejectedAfterReopening(t *testing.T) {
	r, caller, key := replayAssignmentFixture()
	r.attempt.Phase, r.attempt.Rejection = AssignmentRejected, "assignment_in_progress"
	r.order.Status = AwaitingCourier
	s := NewAssigner(r, &revertObservationCredit{}, AssignmentOptions{})
	result, err := s.AcceptOrder(context.Background(), caller, r.order.ID, key)
	if err != nil || result.Phase != AssignmentRejected || result.AttemptID != r.attempt.ID || r.writes != 0 {
		t.Fatalf("rejected retry became a new attempt: %+v %v writes=%d", result, err, r.writes)
	}
}

// Source: R1.2.2/R1.6.12 and the approved immutable-offer acceptance policy.
// Matching an expected status must not permit changing another courier's offer.
func TestAssignmentOfferPolicyRejectsForgedChanges(t *testing.T) {
	r, caller, _ := replayAssignmentFixture()
	r.offers[0].Status = OfferOpen
	old := r.offers[0]
	attempt := r.attempt
	attempt.Phase = AdjustmentPending
	attempt.CallerID = r.order.RequesterID
	for _, test := range []struct {
		name    string
		change  Change
		attempt *AssignmentAttempt
	}{
		{"replace another courier", Change{Action: ProposeOffer, Actor: Actor{Kind: User, ID: uuid.NewString()}, Offers: []OfferChange{{ExpectedStatus: OfferOpen, Offer: Offer{ID: old.ID, OrderID: old.OrderID, CourierID: caller.ID, Credits: 50, Status: OfferVoid}}}}, nil},
		{"change accepted price", Change{Action: BeginAssignment, Actor: Actor{Kind: User, ID: r.order.RequesterID}, Offers: []OfferChange{{ExpectedStatus: OfferOpen, Offer: Offer{ID: old.ID, OrderID: old.OrderID, CourierID: caller.ID, Credits: 60, Status: OfferAccepting}}}}, &attempt},
		{"accept without attempt", Change{Action: BeginAssignment, Actor: Actor{Kind: User, ID: r.order.RequesterID}, Offers: []OfferChange{{ExpectedStatus: OfferOpen, Offer: Offer{ID: old.ID, OrderID: old.OrderID, CourierID: caller.ID, Credits: 50, Status: OfferAccepting}}}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAssignmentOffers(r.order, r.offers, test.change, test.attempt); err == nil {
				t.Fatal("forged offer mutation accepted")
			}
		})
	}
}

// Source: persisted financial intent is not a rejected request when recovery
// encounters a processing/CAS failure. The caller must retain its retry identity.
func TestAdmittedAssignmentProcessingFailureRemainsUnavailable(t *testing.T) {
	r, caller, _ := replayAssignmentFixture()
	r.attempt.Phase = AdjustmentPending
	// This fixture intentionally reports no active owner, causing a processing
	// conflict after admission. It does not simulate a completed Credit transfer.
	_, err := NewAssigner(r, &revertObservationCredit{}, AssignmentOptions{}).resumeAssignment(context.Background(), caller, r.attempt)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("processing conflict became a definitive business rejection: %v", err)
	}
}
