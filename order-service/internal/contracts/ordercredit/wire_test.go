package ordercredit_test

import (
	"testing"

	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// These numbers/types come from Order #123 a0b1e801 and Credit #129 75d60d4f.
// Renamed Credit identifiers deliberately change source/JSON names only.
func TestExistingFieldNumbersAndTypes(t *testing.T) {
	type field struct {
		name   protoreflect.Name
		number protoreflect.FieldNumber
		kind   protoreflect.Kind
	}
	const str = protoreflect.StringKind
	const msg = protoreflect.MessageKind
	const i64 = protoreflect.Int64Kind
	const enum = protoreflect.EnumKind
	for _, row := range []struct {
		message proto.Message
		fields  []field
	}{
		{&orderpb.CreateOrderRequest{}, []field{
			{"pickup_location_id", 1, str}, {"delivery", 2, msg}, {"item_description", 3, str}, {"item_image_ids", 4, str},
			{"reward_credits", 5, i64}, {"delivery_duration", 6, msg}, {"expires_at", 7, msg}, {"idempotency_key", 8, str},
		}},
		{&orderpb.MakeOfferRequest{}, []field{{"order_id", 1, str}, {"credits", 2, i64}, {"idempotency_key", 3, str}}},
		{&orderpb.AcceptOrderRequest{}, []field{{"id", 1, str}, {"idempotency_key", 2, str}}},
		{&orderpb.AcceptOfferRequest{}, []field{{"id", 1, str}, {"idempotency_key", 2, str}}},
		{&orderpb.CancelOrderRequest{}, []field{{"id", 1, str}, {"reason", 2, str}, {"idempotency_key", 3, str}}},
		{&orderpb.AdminCancelOrderRequest{}, []field{{"id", 1, str}, {"reason", 2, str}, {"idempotency_key", 3, str}}},
		{&orderpb.UnassignCourierRequest{}, []field{{"order_id", 1, str}, {"reason", 2, str}, {"idempotency_key", 3, str}}},
		{&creditpb.ReserveRequest{}, []field{{"order_id", 1, str}, {"requester_id", 2, str}, {"amount", 3, i64}}},
		{&creditpb.AdjustReservationRequest{}, []field{{"order_id", 1, str}, {"assignment_id", 2, str}, {"courier_id", 3, str}, {"offer_amount", 4, i64}}},
		{&creditpb.RevertAdjustmentRequest{}, []field{{"order_id", 1, str}, {"assignment_id", 2, str}}},
		{&creditpb.ReleaseReservationRequest{}, []field{{"order_id", 1, str}, {"reason", 2, enum}}},
		{&creditpb.SettleReservationRequest{}, []field{{"order_id", 1, str}, {"completed", 2, msg}, {"cancelled_after_pickup", 3, msg}}},
		{&creditpb.SettleReservationRequest_Completed{}, []field{{"picked_up_at", 1, msg}, {"delivery_duration", 2, msg}, {"delivered_at", 3, msg}}},
		{&creditpb.GetReservationRequest{}, []field{{"order_id", 1, str}}},
		{&creditpb.Reservation{}, []field{
			{"order_id", 1, str}, {"requester_id", 2, str}, {"status", 3, enum}, {"original_amount", 4, i64},
			{"reserved_amount", 5, i64}, {"active_adjustment", 6, msg}, {"settlement", 7, msg}, {"revision", 8, i64},
			{"created_at", 9, msg}, {"updated_at", 10, msg},
		}},
		{&creditpb.Adjustment{}, []field{{"assignment_id", 1, str}, {"courier_id", 2, str}, {"previous_amount", 3, i64}, {"offer_amount", 4, i64}, {"status", 5, enum}}},
		{&creditpb.LedgerEntry{}, []field{
			{"id", 1, str}, {"kind", 2, enum}, {"balance_delta", 3, i64}, {"reserved_delta", 4, i64},
			{"order_id", 5, str}, {"assignment_id", 6, str}, {"created_at", 7, msg},
		}},
	} {
		t.Run(string(row.message.ProtoReflect().Descriptor().FullName()), func(t *testing.T) {
			fields := row.message.ProtoReflect().Descriptor().Fields()
			for _, want := range row.fields {
				got := fields.ByName(want.name)
				if got == nil || got.Number() != want.number || got.Kind() != want.kind {
					t.Errorf("%s must keep number=%d kind=%s, got %v", want.name, want.number, want.kind, got)
				}
			}
			for _, old := range []protoreflect.Name{"request_id", "offer_id"} {
				if fields.ByName(old) != nil {
					t.Errorf("draft identifier %s must use its permanent Order/assignment name", old)
				}
			}
		})
	}
}

func TestCreditDraftIdentifierWireCompatibility(t *testing.T) {
	// Construct draft request_id=1 and offer_id=2 independently of generated tags.
	var wire []byte
	for number, value := range map[protowire.Number]string{1: orderID, 2: locationID, 3: userID} {
		wire = protowire.AppendTag(wire, number, protowire.BytesType)
		wire = protowire.AppendString(wire, value)
	}
	wire = protowire.AppendTag(wire, 4, protowire.VarintType)
	wire = protowire.AppendVarint(wire, 15)
	var adjustment creditpb.AdjustReservationRequest
	if err := proto.Unmarshal(wire, &adjustment); err != nil {
		t.Fatal(err)
	}
	if adjustment.OrderId != orderID || adjustment.AssignmentId != locationID || adjustment.CourierId != userID || adjustment.OfferAmount != 15 {
		t.Fatalf("draft assignment bytes changed meaning: %v", &adjustment)
	}

	wire = nil
	for number, value := range map[protowire.Number]string{5: orderID, 6: locationID} {
		wire = protowire.AppendTag(wire, number, protowire.BytesType)
		wire = protowire.AppendString(wire, value)
	}
	var entry creditpb.LedgerEntry
	if err := proto.Unmarshal(wire, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.OrderId == nil || *entry.OrderId != orderID || entry.AssignmentId == nil || *entry.AssignmentId != locationID {
		t.Fatalf("draft ledger bytes/presence changed meaning: %v", &entry)
	}
}
