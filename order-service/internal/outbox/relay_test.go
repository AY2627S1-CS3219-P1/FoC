package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"google.golang.org/protobuf/proto"
)

func TestOutcomeMapping(t *testing.T) {
	now := time.Now()
	courier := "courier"
	pickup := now.Add(-time.Minute)
	for _, tc := range []struct {
		status app.Status
		after  bool
		want   events.OrderOutcomeType
	}{{app.Completed, true, events.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED}, {app.Cancelled, false, events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP}, {app.Cancelled, true, events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP}, {app.Expired, false, events.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED}, {app.Failed, true, events.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED}} {
		p := app.FactsPayload{OrderID: "order", Revision: 4, RequesterID: "requester", CourierID: &courier, Status: tc.status, AfterPickup: tc.after, PickedUpAt: &pickup, DeliveredAt: &now, DeliveryDurationNS: int64(time.Minute), ReservedCredits: 17, At: now}
		payload, _ := json.Marshal(p)
		message, err := encode(Fact{ID: "event", OrderID: "order", Revision: 4, Kind: string(app.Outcome), Version: 1, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		event := message.(*events.OrderOutcome)
		if event.Outcome != tc.want || event.ReservedCredits != 17 || event.EventId != "event" {
			t.Fatalf("%+v", event)
		}
	}
	for _, status := range []app.Status{app.Rejected, app.PendingReservation, app.Completed} {
		if _, err := outcomeType(app.FactsPayload{Status: status}); err == nil {
			t.Fatalf("accepted invalid terminal payload %s", status)
		}
	}
}

type memoryFacts struct {
	fact       *Fact
	ackFailure bool
	published  bool
}

func (m *memoryFacts) Claim(context.Context, time.Time, time.Time) (*Fact, error) {
	if m.published {
		return nil, nil
	}
	return m.fact, nil
}

func (m *memoryFacts) Published(context.Context, Fact, time.Time) error {
	if m.ackFailure {
		m.ackFailure = false
		return errors.New("local commit lost")
	}
	m.published = true
	return nil
}

func (m *memoryFacts) Retry(context.Context, Fact, time.Time) error { return nil }

type recordingPublisher struct {
	ids  []string
	fail bool
}

func (p *recordingPublisher) Publish(_ context.Context, _ string, key []byte, m proto.Message) error {
	event := m.(*events.OrderStatusChanged)
	if string(key) != event.OrderId {
		return errors.New("wrong key")
	}
	p.ids = append(p.ids, event.EventId)
	if p.fail {
		p.fail = false
		return errors.New("broker response lost")
	}
	return nil
}

func TestPublicationCrashReplaysOriginalIdentity(t *testing.T) {
	payload, _ := json.Marshal(app.FactsPayload{OrderID: "order", Revision: 1, RequesterID: "requester", Status: app.PendingReservation, At: time.Now()})
	for _, localFailure := range []bool{false, true} {
		store := &memoryFacts{fact: &Fact{ID: "stable-event", OrderID: "order", Revision: 1, Version: 1, Kind: string(app.StatusChanged), Payload: payload, ActorKind: "user"}, ackFailure: localFailure}
		publisher := &recordingPublisher{fail: !localFailure}
		if err := New(store, publisher).Drain(context.Background(), 1); err == nil || store.published {
			t.Fatal("uncertain delivery was acknowledged")
		}
		if err := New(store, publisher).Drain(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if !store.published || len(publisher.ids) != 2 || publisher.ids[0] != publisher.ids[1] {
			t.Fatal("recovery changed event identity")
		}
	}
}

type blockedClaims struct {
	Repository
	started chan struct{}
}

func (b blockedClaims) Claim(ctx context.Context, _ time.Time, _ time.Time) (*Fact, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRelayShutdownCancelsClaim(t *testing.T) {
	store := blockedClaims{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); New(store, &recordingPublisher{}).Run(ctx) }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("relay did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop")
	}
}
