package ordercredit_test

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const orderID = "37dbf670-f32c-4cca-9473-5861c274e789"
const userID = "c55ce32c-2af7-46d1-a088-6a976d3bdbaf"
const locationID = "41dbf670-f32c-4cca-9473-5861c274e789"

func creation() *orderpb.CreateOrderRequest {
	return &orderpb.CreateOrderRequest{
		PickupLocationId: locationID,
		Delivery:         &orderpb.DeliveryInput{LocationId: orderID},
		ItemDescription:  "Book", RewardCredits: 10,
		DeliveryDuration: durationpb.New(10 * time.Minute),
		// Deliberately expired: deadline eligibility belongs after workflow lookup,
		// so retained retries must still reach the future creation operation.
		ExpiresAt:      timestamppb.New(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)),
		IdempotencyKey: userID,
	}
}

func completedSettlement() *creditpb.SettleReservationRequest {
	pickup := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	return &creditpb.SettleReservationRequest{
		OrderId: orderID,
		Outcome: &creditpb.SettleReservationRequest_Completed_{
			Completed: &creditpb.SettleReservationRequest_Completed{
				PickedUpAt: timestamppb.New(pickup), DeliveryDuration: durationpb.New(10 * time.Minute),
				DeliveredAt: timestamppb.New(pickup.Add(12 * time.Minute)),
			},
		},
	}
}

func TestRequiredRetryKeysAndOperationIdentifiers(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	requests := []proto.Message{
		creation(),
		&orderpb.MakeOfferRequest{OrderId: orderID, Credits: 15, IdempotencyKey: userID},
		&orderpb.AcceptOrderRequest{Id: orderID, IdempotencyKey: userID},
		&orderpb.AcceptOfferRequest{Id: orderID, IdempotencyKey: userID},
		&orderpb.CancelOrderRequest{Id: orderID, Reason: proto.String("Changed plans"), IdempotencyKey: userID},
		&orderpb.AdminCancelOrderRequest{Id: orderID, Reason: "Suspension", IdempotencyKey: userID},
		&orderpb.UnassignCourierRequest{OrderId: orderID, Reason: "Suspension", IdempotencyKey: userID},
		&creditpb.ReserveRequest{OrderId: orderID, RequesterId: userID, Amount: 10},
		&creditpb.AdjustReservationRequest{OrderId: orderID, AssignmentId: locationID, CourierId: userID, OfferAmount: 15},
		&creditpb.RevertAdjustmentRequest{OrderId: orderID, AssignmentId: locationID},
		&creditpb.ReleaseReservationRequest{OrderId: orderID, Reason: creditpb.ReleaseReason_RELEASE_REASON_EXPIRED},
		completedSettlement(),
		&creditpb.GetReservationRequest{OrderId: orderID},
	}
	identifiers := map[protoreflect.Name]bool{
		"id": true, "order_id": true, "assignment_id": true, "requester_id": true,
		"courier_id": true, "pickup_location_id": true, "idempotency_key": true,
	}
	for _, request := range requests {
		t.Run(string(request.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			if err := validator.Validate(request); err != nil {
				t.Fatalf("valid request: %v", err)
			}
			fields := request.ProtoReflect().Descriptor().Fields()
			for i := 0; i < fields.Len(); i++ {
				field := fields.Get(i)
				if !identifiers[field.Name()] {
					continue
				}
				for _, invalid := range []string{"", "not-a-uuid"} {
					t.Run(string(field.Name())+"/"+invalid, func(t *testing.T) {
						changed := proto.Clone(request)
						changed.ProtoReflect().Set(field, protoreflect.ValueOfString(invalid))
						if err := validator.Validate(changed); err == nil {
							t.Fatalf("accepted invalid %s = %q", field.Name(), invalid)
						}
					})
				}
			}
		})
	}
}

func TestCreationDeadlineValidationAllowsRetainedReplay(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name  string
		edit  func(*orderpb.CreateOrderRequest)
		valid bool
	}{
		{"past deadline passes shape validation", func(*orderpb.CreateOrderRequest) {}, true},
		{"missing deadline", func(r *orderpb.CreateOrderRequest) { r.ExpiresAt = nil }, false},
		{"missing duration", func(r *orderpb.CreateOrderRequest) { r.DeliveryDuration = nil }, false},
		{"short duration", func(r *orderpb.CreateOrderRequest) { r.DeliveryDuration = durationpb.New(time.Minute) }, false},
		{"invalid delivery location", func(r *orderpb.CreateOrderRequest) { r.Delivery.LocationId = "invalid" }, false},
		{"same locations", func(r *orderpb.CreateOrderRequest) { r.Delivery.LocationId = r.PickupLocationId }, false},
		{"invalid image", func(r *orderpb.CreateOrderRequest) { r.ItemImageIds = []string{"invalid"} }, false},
		{"zero reward", func(r *orderpb.CreateOrderRequest) { r.RewardCredits = 0 }, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			request := creation()
			row.edit(request)
			if err := validator.Validate(request); (err == nil) != row.valid {
				t.Fatalf("valid = %v, error = %v", row.valid, err)
			}
		})
	}
}

func TestCreditFinancialInputValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name    string
		request proto.Message
	}{
		{"zero reserve", &creditpb.ReserveRequest{OrderId: orderID, RequesterId: userID}},
		{"negative adjustment", &creditpb.AdjustReservationRequest{OrderId: orderID, AssignmentId: locationID, CourierId: userID, OfferAmount: -1}},
		{"missing release reason", &creditpb.ReleaseReservationRequest{OrderId: orderID}},
		{"unknown release reason", &creditpb.ReleaseReservationRequest{OrderId: orderID, Reason: creditpb.ReleaseReason(999)}},
		{"missing settlement outcome", &creditpb.SettleReservationRequest{OrderId: orderID}},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := validator.Validate(row.request); err == nil {
				t.Fatal("accepted invalid financial input")
			}
		})
	}
	for _, row := range []struct {
		name  string
		edit  func(*creditpb.SettleReservationRequest_Completed)
		valid bool
	}{
		{"valid delivery observations", func(*creditpb.SettleReservationRequest_Completed) {}, true},
		{"missing pickup", func(c *creditpb.SettleReservationRequest_Completed) { c.PickedUpAt = nil }, false},
		{"missing delivery", func(c *creditpb.SettleReservationRequest_Completed) { c.DeliveredAt = nil }, false},
		{"missing duration", func(c *creditpb.SettleReservationRequest_Completed) { c.DeliveryDuration = nil }, false},
		{"zero duration", func(c *creditpb.SettleReservationRequest_Completed) { c.DeliveryDuration = durationpb.New(0) }, false},
		{"delivery before pickup", func(c *creditpb.SettleReservationRequest_Completed) {
			c.DeliveredAt = timestamppb.New(c.PickedUpAt.AsTime().Add(-time.Second))
		}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			request := completedSettlement()
			row.edit(request.GetCompleted())
			if err := validator.Validate(request); (err == nil) != row.valid {
				t.Fatalf("valid = %v, error = %v", row.valid, err)
			}
		})
	}
	cancelled := &creditpb.SettleReservationRequest{
		OrderId: orderID,
		Outcome: &creditpb.SettleReservationRequest_CancelledAfterPickup_{
			CancelledAfterPickup: &creditpb.SettleReservationRequest_CancelledAfterPickup{},
		},
	}
	if err := validator.Validate(cancelled); err != nil {
		t.Fatalf("full-reward cancellation outcome: %v", err)
	}
}
