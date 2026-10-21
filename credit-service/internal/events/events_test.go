package events_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	ordereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	orderID     = "11111111-1111-4111-8111-111111111111"
	requesterID = "22222222-2222-4222-8222-222222222222"
	courierID   = "33333333-3333-4333-8333-333333333333"
)

var meta = kafka.Metadata{Topic: events.OrderOutcomeTopic}

// fakeSettler returns errs in turn, then succeeds.
type fakeSettler struct {
	errs     []error
	outcomes []reservation.OrderOutcome
}

func (f *fakeSettler) ApplyOutcome(_ context.Context, o reservation.OrderOutcome) (reservation.Reservation, bool, error) {
	f.outcomes = append(f.outcomes, o)
	if len(f.errs) == 0 {
		return reservation.Reservation{}, true, nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return reservation.Reservation{}, false, err
}

func completed() *ordereventsv1.OrderOutcome {
	picked := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	return &ordereventsv1.OrderOutcome{
		EventId:          "event",
		OrderId:          orderID,
		Outcome:          ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED,
		RequesterId:      requesterID,
		CourierId:        proto.String(courierID),
		ReservedCredits:  30,
		DeliveryDuration: durationpb.New(time.Hour),
		PickedUpAt:       timestamppb.New(picked),
		DeliveredAt:      timestamppb.New(picked.Add(90 * time.Minute)),
	}
}

// Outcome conversion

func TestOutcomeConvertsEveryOutcomeType(t *testing.T) {
	tests := map[ordereventsv1.OrderOutcomeType]reservation.OutcomeType{
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED:               reservation.OrderCompleted,
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP: reservation.OrderCancelledBeforePickup,
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP:  reservation.OrderCancelledAfterPickup,
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED:                 reservation.OrderExpired,
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED:                  reservation.OrderFailed,
		ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_UNSPECIFIED:             "",
	}
	for in, want := range tests {
		if got := events.Outcome(&ordereventsv1.OrderOutcome{Outcome: in}).Type; got != want {
			t.Errorf("%s became %q, want %q", in, got, want)
		}
	}
}

func TestOutcomeCarriesLatenessInputs(t *testing.T) {
	o := events.Outcome(completed())

	if o.OrderID != orderID || o.RequesterID != requesterID || o.CourierID != courierID {
		t.Fatalf("ids: %+v", o)
	}
	if o.DeliveryDuration != time.Hour || o.DeliveredAt.Sub(o.PickedUpAt) != 90*time.Minute {
		t.Fatalf("lateness: %+v", o)
	}

	expired := events.Outcome(&ordereventsv1.OrderOutcome{OrderId: orderID, Outcome: ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED})
	if expired.CourierID != "" || !expired.PickedUpAt.IsZero() || expired.DeliveryDuration != 0 {
		t.Fatalf("unset fields: %+v", expired)
	}
}

// Outcome handling

func TestOutcomesSettle(t *testing.T) {
	settler := &fakeSettler{}
	h := &events.Outcomes{Settler: settler}

	if err := h.Handle(context.Background(), meta, completed()); err != nil {
		t.Fatal(err)
	}
	if len(settler.outcomes) != 1 || settler.outcomes[0].Type != reservation.OrderCompleted {
		t.Fatalf("outcomes %+v", settler.outcomes)
	}
}

func TestOutcomesAcknowledgeMismatches(t *testing.T) {
	for _, err := range []error{
		reservation.ErrOutcomeMismatch,
		reservation.ErrReservationNotFound,
		reservation.ErrReservationNotAssigned,
		reservation.ErrInvalidArgument,
	} {
		h := &events.Outcomes{Settler: &fakeSettler{errs: []error{err}}}
		if got := h.Handle(context.Background(), meta, completed()); got != nil {
			t.Errorf("%v: %v", err, got)
		}
	}
}

func TestOutcomesRetryAbortedSettlement(t *testing.T) {
	aborted := fmt.Errorf("commit: %w", reservation.ErrAborted)
	settler := &fakeSettler{errs: []error{aborted, aborted}}
	h := &events.Outcomes{Settler: settler}

	if err := h.Handle(context.Background(), meta, completed()); err != nil || len(settler.outcomes) != 3 {
		t.Fatalf("err %v after %d attempts", err, len(settler.outcomes))
	}

	settler = &fakeSettler{errs: []error{aborted, aborted, aborted}}
	h = &events.Outcomes{Settler: settler}
	if err := h.Handle(context.Background(), meta, completed()); !errors.Is(err, reservation.ErrAborted) || len(settler.outcomes) != 3 {
		t.Fatalf("err %v after %d attempts", err, len(settler.outcomes))
	}
}

func TestOutcomesReturnInfrastructureFailures(t *testing.T) {
	down := errors.New("connection refused")
	h := &events.Outcomes{Settler: &fakeSettler{errs: []error{down}}}

	if err := h.Handle(context.Background(), meta, completed()); !errors.Is(err, down) {
		t.Fatalf("err %v", err)
	}
}

// User creation

type fakeOpener struct {
	opened []string
	err    error
}

func (f *fakeOpener) Open(_ context.Context, userID string) error {
	f.opened = append(f.opened, userID)
	return f.err
}

func TestUsersOpenAccounts(t *testing.T) {
	opener := &fakeOpener{}
	h := &events.Users{Opener: opener}

	if err := h.Handle(context.Background(), kafka.Metadata{}, &userseventsv1.UserCreated{EventId: "event", UserId: requesterID}); err != nil {
		t.Fatal(err)
	}
	if len(opener.opened) != 1 || opener.opened[0] != requesterID {
		t.Fatalf("opened %v", opener.opened)
	}
}

func TestUsersAcknowledgeInvalidIDsAndReturnFailures(t *testing.T) {
	h := &events.Users{Opener: &fakeOpener{err: account.ErrInvalidArgument}}
	if err := h.Handle(context.Background(), kafka.Metadata{}, &userseventsv1.UserCreated{UserId: "nope"}); err != nil {
		t.Fatalf("invalid id: %v", err)
	}

	down := errors.New("connection refused")
	h = &events.Users{Opener: &fakeOpener{err: down}}
	if err := h.Handle(context.Background(), kafka.Metadata{}, &userseventsv1.UserCreated{UserId: requesterID}); !errors.Is(err, down) {
		t.Fatalf("failure: %v", err)
	}
}
