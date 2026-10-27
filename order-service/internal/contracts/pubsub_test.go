package contracts_test

import (
	"encoding/hex"
	"testing"

	"buf.build/go/protovalidate"
	credit "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	order "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestOriginalIdentitiesAndMutations(t *testing.T) {
	for _, m := range []proto.Message{&order.AcceptOrderRequest{}, &order.AcceptOfferRequest{}, &order.AdminCancelOrderRequest{}, &order.UnassignCourierRequest{}} {
		if m.ProtoReflect().Descriptor().Fields().ByName("idempotency_key") != nil {
			t.Fatalf("unexpected new retry key: %T", m)
		}
	}
	for _, tc := range []struct {
		m      proto.Message
		name   string
		number protoreflect.FieldNumber
	}{{&credit.ReserveRequest{}, "request_id", 1}, {&credit.AdjustReservationRequest{}, "offer_id", 2}, {&order.CreateOrderRequest{}, "idempotency_key", 8}, {&order.CancelOrderRequest{}, "idempotency_key", 3}} {
		f := tc.m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(tc.name))
		if f == nil || f.Number() != tc.number {
			t.Fatalf("changed identity: %T %s", tc.m, tc.name)
		}
	}
}

func TestUploadLimitsAndFormats(t *testing.T) {
	for _, media := range []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"} {
		for _, size := range []int64{1, 10000000} {
			if err := protovalidate.Validate(&order.CreateItemImageUploadRequest{ContentType: media, SizeBytes: size}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, size := range []int64{-1, 0, 10000001} {
		if err := protovalidate.Validate(&order.CreateItemImageUploadRequest{ContentType: "image/png", SizeBytes: size}); err == nil {
			t.Fatalf("accepted size %d", size)
		}
	}
}

func TestOutcomeKeepsOriginalWireFields(t *testing.T) {
	// Original OrderOutcome wire fields: event_id=1, order_id=2, outcome=3,
	// requester_id=4 and reserved_credits=6. Keep this fixture independent
	// of the removed draft RPC event types.
	wire, err := hex.DecodeString("0a056576656e7412056f7264657218012209726571756573746572300d")
	if err != nil {
		t.Fatal(err)
	}
	var event events.OrderOutcome
	if err := proto.Unmarshal(wire, &event); err != nil {
		t.Fatal(err)
	}
	if event.EventId != "event" || event.OrderId != "order" || event.ReservedCredits != 13 || event.Outcome != events.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED {
		t.Fatal("outcome wire contract changed")
	}
}

func TestMalformedReservationAndRetryIdentifiers(t *testing.T) {
	for _, m := range []proto.Message{
		&credit.ReserveRequest{RequestId: "bad", RequesterId: "49ae3b41-c448-4f6a-9a74-4d23e615b55d", Amount: 10},
		&credit.ReserveRequest{RequestId: "49ae3b41-c448-4f6a-9a74-4d23e615b55d", RequesterId: "bad", Amount: 10},
		&order.AcceptOrderRequest{Id: "bad"},
		&order.MakeOfferRequest{OrderId: "49ae3b41-c448-4f6a-9a74-4d23e615b55d", Credits: 10, IdempotencyKey: "bad"},
		&order.GetOrderRequest{Id: ""},
	} {
		if err := protovalidate.Validate(m); err == nil {
			t.Fatalf("accepted malformed identifiers in %T", m)
		}
	}
}
