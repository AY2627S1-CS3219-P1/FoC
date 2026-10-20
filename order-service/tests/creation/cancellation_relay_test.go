//go:build integration

package creation_test

import (
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// Reuse creation's disposable PostgreSQL/Kafka harness and the production relay.
// Credit is scripted; Kafka publication does not certify real Credit settlement.
func TestCancellationOutboxWithKafka(t *testing.T) {
	f := databaseFixture(t)
	address := kafkaBroker(t, f.ctx)
	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: []string{address}})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	for _, who := range []string{"requester before pickup", "requester after pickup", "courier after pickup"} {
		t.Run(who, func(t *testing.T) {
			s := newScenario(t, f)
			c := mount(t, f, s.creator)
			created, err := create(c, f.ctx, input(s.clock.Now()))
			if err != nil {
				t.Fatal(err)
			}
			courier := uuid.NewString()
			if who != "requester before pickup" {
				for _, action := range []app.Action{app.Assign, app.Pickup} {
					s.clock.Advance(time.Second)
					err := app.NewMutator(s.repo).Mutate(f.ctx, created.Id, func(o app.Order, _ []app.Offer) (*app.Change, error) {
						after := o
						now := s.clock.Now()
						after.Revision++
						after.UpdatedAt = now
						if action == app.Assign {
							after.Status = app.AwaitingPickup
							after.CourierID = &courier
							after.AssignedAt = &now
						} else {
							after.Status = app.Delivering
							after.PickedUpAt = &now
						}
						change, err := app.NewChange(&o, after, app.Actor{Kind: app.User, ID: courier}, action, nil, uuid.NewString)
						return &change, err
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			// Drain preceding lifecycle facts, then count only cancellation publications.
			relay := outbox.New(outbox.NewPostgres(f.pool), publisher)
			if err := relay.Drain(f.ctx, 50); err != nil {
				t.Fatal(err)
			}
			statuses := topicOffset(t, address, "order.status_changed")
			outcomes := topicOffset(t, address, "order.outcome")
			warnings := topicOffset(t, address, "order.courier_warning_requested")
			caller := app.Caller{ID: created.RequesterId, Suspended: true}
			expectedStatus := events.OrderStatus_ORDER_STATUS_CANCELLED
			expectedOutcome := events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP
			if who == "requester after pickup" {
				expectedOutcome = events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP
			}
			if who == "courier after pickup" {
				caller.ID = courier
				expectedStatus = events.OrderStatus_ORDER_STATUS_FAILED
				expectedOutcome = events.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED
			}
			s.clock.Advance(time.Second)
			canceller := app.NewCanceller(s.repo, nil, app.AssignmentOptions{Now: s.clock.Now})
			key := uuid.NewString()
			for range 2 {
				result, err := canceller.Cancel(f.ctx, caller, created.Id, "cancel", key)
				if err != nil || result.Status != app.CancellationSucceeded {
					t.Fatalf("cancellation %+v %v", result, err)
				}
			}
			if err := relay.Drain(f.ctx, 50); err != nil {
				t.Fatal(err)
			}
			var status events.OrderStatusChanged
			message := topicMessages(t, address, "order.status_changed", statuses, 1)[0]
			if err := proto.Unmarshal(message.Value, &status); err != nil {
				t.Fatal(err)
			}
			if status.Status != expectedStatus || status.OrderId != created.Id || string(message.Key) != created.Id || status.Actor.UserId == nil || *status.Actor.UserId != caller.ID {
				t.Fatalf("status %v", &status)
			}
			var outcome events.OrderOutcome
			message = topicMessages(t, address, "order.outcome", outcomes, 1)[0]
			if err := proto.Unmarshal(message.Value, &outcome); err != nil {
				t.Fatal(err)
			}
			if outcome.Outcome != expectedOutcome || outcome.OrderId != created.Id || outcome.ReservedCredits != 10 || string(message.Key) != created.Id || (outcome.PickedUpAt == nil) != (who == "requester before pickup") {
				t.Fatalf("outcome %v", &outcome)
			}
			if who == "courier after pickup" {
				var warning events.CourierWarningRequested
				message = topicMessages(t, address, "order.courier_warning_requested", warnings, 1)[0]
				if err := proto.Unmarshal(message.Value, &warning); err != nil {
					t.Fatal(err)
				}
				if warning.CourierId != courier || warning.OrderId != created.Id {
					t.Fatalf("warning %v", &warning)
				}
				warnings++
			}
			if topicOffset(t, address, "order.status_changed") != statuses+1 || topicOffset(t, address, "order.outcome") != outcomes+1 || topicOffset(t, address, "order.courier_warning_requested") != warnings {
				t.Fatal("duplicate or unexpected events")
			}
			if s.remotes.releaseCalls != 0 {
				t.Fatal("terminal cancellation called ReleaseReservation")
			}
		})
	}
}
