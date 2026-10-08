package location_test

import (
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDisablementFieldCompatibility(t *testing.T) {
	start := timestamppb.New(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	end := timestamppb.New(start.AsTime().Add(time.Hour))
	// Encode the four-field discovery resource, independent of our generated tags.
	var wire []byte
	for _, field := range []struct {
		number protowire.Number
		value  []byte
	}{{1, []byte("interval-id")}, {2, marshalProto(t, start)}, {3, marshalProto(t, end)}, {4, []byte("Maintenance")}} {
		wire = protowire.AppendTag(wire, field.number, protowire.BytesType)
		wire = protowire.AppendBytes(wire, field.value)
	}
	var got pb.Disablement
	if err := proto.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Id != "interval-id" || !proto.Equal(got.StartsAt, start) || !proto.Equal(got.EndsAt, end) || got.Reason != "Maintenance" || got.LocationId != "" {
		t.Fatalf("discovery resource changed meaning: %v", &got)
	}
	fields := got.ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"id": 1, "starts_at": 2, "ends_at": 3, "reason": 4,
		"location_id": 5, "ended_at": 6, "cancelled_at": 7, "created_by": 8,
		"state": 9, "revision": 10, "created_at": 11, "updated_at": 12,
	} {
		if field := fields.ByName(name); field == nil || field.Number() != number {
			t.Fatalf("field %s must retain number %d", name, number)
		}
	}
}

func marshalProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	wire, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestSharedLocationInputValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	const id = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	base := &pb.LocationInput{
		Name: "Campus cafe", IsSupplier: proto.Bool(true), CategoryIds: []string{id}, BuildingId: id,
		Coordinates: &pb.Coordinates{Latitude: 1.294, Longitude: 103.774},
		OpensAt:     &timeofday.TimeOfDay{Hours: 22, Minutes: 5},
		ClosesAt:    &timeofday.TimeOfDay{Hours: 2},
	}
	create := func(edit func(*pb.LocationInput)) *pb.CreateLocationRequest {
		input := proto.Clone(base).(*pb.LocationInput)
		edit(input)
		return &pb.CreateLocationRequest{IdempotencyKey: id, Location: input}
	}
	for _, row := range []struct {
		name    string
		message proto.Message
		valid   bool
	}{
		{"accept overnight opening hours", create(func(*pb.LocationInput) {}), true},
		{"accept 200 Unicode name characters after trimming", create(func(p *pb.LocationInput) { p.Name = "\u2003" + strings.Repeat("é", 200) + "\u2003" }), true},
		{"reject 201 Unicode name characters after trimming", create(func(p *pb.LocationInput) { p.Name = strings.Repeat("é", 201) }), false},
		{"reject whitespace name", create(func(p *pb.LocationInput) { p.Name = " \u2003 " }), false},
		{"accept 50 character floor after trimming", create(func(p *pb.LocationInput) { p.Floor = proto.String(" " + strings.Repeat("F", 50) + " ") }), true},
		{"reject 51 character floor after trimming", create(func(p *pb.LocationInput) { p.Floor = proto.String(strings.Repeat("F", 51)) }), false},
		{"accept 500 character contact after trimming", create(func(p *pb.LocationInput) { p.Contact = proto.String(" " + strings.Repeat("c", 500) + " ") }), true},
		{"reject 501 character contact after trimming", create(func(p *pb.LocationInput) { p.Contact = proto.String(strings.Repeat("c", 501)) }), false},
		{"accept 2000 character details after trimming", create(func(p *pb.LocationInput) { p.Details = " " + strings.Repeat("d", 2000) + " " }), true},
		{"reject 2001 character details after trimming", create(func(p *pb.LocationInput) { p.Details = strings.Repeat("d", 2001) }), false},
		{"accept omitted opening hours", create(func(p *pb.LocationInput) { p.OpensAt, p.ClosesAt = nil, nil }), true},
		{"reject opening time without closing time", create(func(p *pb.LocationInput) { p.ClosesAt = nil }), false},
		{"reject equal opening and closing times", create(func(p *pb.LocationInput) { p.ClosesAt = proto.Clone(p.OpensAt).(*timeofday.TimeOfDay) }), false},
		{"reject hour outside valid range", create(func(p *pb.LocationInput) { p.OpensAt.Hours = 24 }), false},
		{"reject seconds in opening hours", create(func(p *pb.LocationInput) { p.OpensAt.Seconds = 3 }), false},
		{"reject microseconds in opening hours", create(func(p *pb.LocationInput) { p.OpensAt.Nanos = 456000 }), false},
		{"reject nanoseconds in opening hours", create(func(p *pb.LocationInput) { p.OpensAt.Nanos = 1 }), false},
		{"defer supplier without Categories to domain", create(func(p *pb.LocationInput) { p.CategoryIds = nil }), true},
		{"defer non-supplier Location with Categories to domain", create(func(p *pb.LocationInput) { p.IsSupplier = proto.Bool(false) }), true},
		{"accept non-supplier Location without Categories", create(func(p *pb.LocationInput) { p.IsSupplier = proto.Bool(false); p.CategoryIds = nil }), true},
		{"reject missing supplier classification", create(func(p *pb.LocationInput) { p.IsSupplier = nil }), false},
		{"reject missing coordinates", create(func(p *pb.LocationInput) { p.Coordinates = nil }), false},
		{"accept floor update without unrelated fields", &pb.UpdateLocationRequest{Id: id, ExpectedRevision: 1, Location: &pb.LocationInput{Floor: proto.String("2")}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}}}, true},
		{"accept clearing both opening hours", &pb.UpdateLocationRequest{Id: id, ExpectedRevision: 1, Location: &pb.LocationInput{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"opens_at", "closes_at"}}}, true},
		{"reject unsupported opening-hours field names", &pb.UpdateLocationRequest{Id: id, ExpectedRevision: 1, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"open_from", "open_to"}}}, false},
		{"reject update to read-only revision", &pb.UpdateLocationRequest{Id: id, ExpectedRevision: 1, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"revision"}}}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := validator.Validate(row.message)
			if (err == nil) != row.valid {
				t.Fatalf("expected valid=%v, got %v", row.valid, err)
			}
			if request, ok := row.message.(*pb.CreateLocationRequest); ok {
				submitted := &pb.SubmitLocationAdditionRequestRequest{Proposal: proto.Clone(request.Location).(*pb.LocationInput), IdempotencyKey: id}
				if err := validator.Validate(submitted); (err == nil) != row.valid {
					t.Fatalf("addition request validation diverged from admin input: %v", err)
				}
			}
		})
	}
}

func TestDisablementUpdateValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	const id = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	for _, row := range []struct {
		name   string
		paths  []string
		reason string
		valid  bool
	}{
		{"accept reason update", []string{"reason"}, "Maintenance", true},
		{"reject empty selected reason", []string{"reason"}, "", false},
		{"accept omitted unselected reason", []string{"ends_at"}, "", true},
		{"reject unknown path", []string{"revision"}, "", false},
		{"reject empty mask", []string{}, "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			request := &pb.UpdateDisablementRequest{Id: id, ExpectedRevision: 1, Reason: row.reason, UpdateMask: &fieldmaskpb.FieldMask{Paths: row.paths}}
			if err := validator.Validate(request); (err == nil) != row.valid {
				t.Fatalf("expected valid=%v, got %v", row.valid, err)
			}
		})
	}
}
