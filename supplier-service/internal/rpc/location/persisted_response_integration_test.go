//go:build integration

package location_test

import (
	"testing"
	"time"

	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// PostgreSQL stores microsecond timestamps. A first mutation response must
// expose the stored resource, just like a later read or idempotent replay.
func TestLifecyclePersistedResponsesPostGIS(t *testing.T) {
	f := newFixture(t)
	f.clockOffset = 417 * time.Nanosecond
	if f.time().Nanosecond()%1000 == 0 {
		t.Fatal("clock must retain sub-microsecond precision")
	}
	t.Run("Create retry and End repeat return exact persisted snapshots", func(t *testing.T) {
		f.reset(t)
		input := &pb.CreateDisablementRequest{LocationId: location, Reason: "real-clock closure", IdempotencyKey: uuid.NewString()}
		first, err := f.location.CreateDisablement(f.ctx, req(input, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		repeat, err := f.location.CreateDisablement(f.ctx, req(input, "admin"))
		if err != nil || !proto.Equal(first.Msg, repeat.Msg) {
			t.Fatalf("Create first versus replay: %v %v err=%v", first, repeat, err)
		}
		f.now.Add(int64(time.Second / time.Microsecond))
		ended, err := f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: first.Msg.Disablement.Id}, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		endedAgain, err := f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: first.Msg.Disablement.Id}, "admin"))
		if err != nil || !proto.Equal(ended.Msg, endedAgain.Msg) {
			t.Fatalf("End first versus replay: %v %v err=%v", ended, endedAgain, err)
		}
		list, err := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location}, "admin"))
		if err != nil || len(list.Msg.Disablements) != 1 || !proto.Equal(ended.Msg.Disablement, list.Msg.Disablements[0]) {
			t.Fatalf("End first versus persisted list: %v err=%v", list, err)
		}
	})
	t.Run("scheduled Update returns exact persisted snapshot", func(t *testing.T) {
		f.reset(t)
		start := f.time().Add(time.Hour)
		d := f.create(t, &start, nil)
		f.now.Add(int64(time.Second / time.Microsecond))
		updated, err := f.location.UpdateDisablement(f.ctx, req(&pb.UpdateDisablementRequest{Id: d.Id, ExpectedRevision: d.Revision, Reason: "changed", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"reason"}}}, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		list, err := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location}, "admin"))
		if err != nil || len(list.Msg.Disablements) != 1 || !proto.Equal(updated.Msg.Disablement, list.Msg.Disablements[0]) {
			t.Fatalf("Update first versus persisted list: %v %v err=%v", updated, list, err)
		}
	})
	t.Run("scheduled Cancel repeat returns exact persisted snapshot", func(t *testing.T) {
		f.reset(t)
		start := f.time().Add(time.Hour)
		d := f.create(t, &start, nil)
		f.now.Add(int64(time.Second / time.Microsecond))
		cancelled, err := f.location.CancelDisablement(f.ctx, req(&pb.CancelDisablementRequest{Id: d.Id}, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.location.CancelDisablement(f.ctx, req(&pb.CancelDisablementRequest{Id: d.Id}, "admin"))
		if err != nil || !proto.Equal(cancelled.Msg, again.Msg) {
			t.Fatalf("Cancel first versus replay: %v %v err=%v", cancelled, again, err)
		}
	})
	t.Run("Submit replay and request Update return exact persisted snapshots", func(t *testing.T) {
		f.reset(t)
		input := &pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: uuid.NewString()}
		first, err := f.requests.SubmitLocationAdditionRequest(f.ctx, req(input, "owner"))
		if err != nil {
			t.Fatal(err)
		}
		repeat, err := f.requests.SubmitLocationAdditionRequest(f.ctx, req(input, "owner"))
		if err != nil || !proto.Equal(first.Msg, repeat.Msg) {
			t.Fatalf("Submit first versus replay: %v %v err=%v", first, repeat, err)
		}
		f.now.Add(int64(time.Second / time.Microsecond))
		updated, err := f.requests.UpdateLocationAdditionRequest(f.ctx, req(&pb.UpdateLocationAdditionRequestRequest{Id: first.Msg.Request.Id, ExpectedRevision: first.Msg.Request.Revision, Proposal: &pb.LocationInput{Details: "changed"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"details"}}}, "owner"))
		if err != nil {
			t.Fatal(err)
		}
		read, err := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: first.Msg.Request.Id}, "owner"))
		if err != nil || !proto.Equal(updated.Msg.Request, read.Msg.Request) {
			t.Fatalf("request Update first versus read: %v %v err=%v", updated, read, err)
		}
	})
	t.Run("Withdraw repeat returns exact persisted snapshot", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		f.now.Add(int64(time.Second / time.Microsecond))
		withdrawn, err := f.requests.WithdrawLocationAdditionRequest(f.ctx, req(&pb.WithdrawLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.requests.WithdrawLocationAdditionRequest(f.ctx, req(&pb.WithdrawLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if err != nil || !proto.Equal(withdrawn.Msg, again.Msg) {
			t.Fatalf("Withdraw first versus replay: %v %v err=%v", withdrawn, again, err)
		}
	})
	t.Run("Reject returns exact persisted review timestamps", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		f.now.Add(int64(time.Second / time.Microsecond))
		input := &pb.RejectLocationAdditionRequestRequest{Id: r.Id, ReviewNote: "not suitable"}
		first, err := f.requests.RejectLocationAdditionRequest(f.ctx, req(input, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		repeat, err := f.requests.RejectLocationAdditionRequest(f.ctx, req(input, "admin"))
		if err != nil || !proto.Equal(first.Msg, repeat.Msg) {
			t.Fatalf("Reject first versus replay: %v %v err=%v", first, repeat, err)
		}
	})
	t.Run("Approve returns exact persisted request and Location", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		f.now.Add(int64(time.Second / time.Microsecond))
		input := &pb.ApproveLocationAdditionRequestRequest{Id: r.Id}
		first, err := f.requests.ApproveLocationAdditionRequest(f.ctx, req(input, "admin"))
		if err != nil {
			t.Fatal(err)
		}
		repeat, err := f.requests.ApproveLocationAdditionRequest(f.ctx, req(input, "admin"))
		if err != nil || !proto.Equal(first.Msg, repeat.Msg) {
			t.Fatalf("Approve first versus replay: %v %v err=%v", first, repeat, err)
		}
	})
}
