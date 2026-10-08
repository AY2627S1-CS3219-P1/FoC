//go:build integration

package location_test

import (
	"context"
	"database/sql"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const building = "9dbda827-5a46-4089-9209-a2bde43f18a0"
const category = "63dc656d-4e7b-43ad-a0ba-325b44132766"
const categoryTwo = "d064dc96-f8be-47ef-a1a3-9c72c05e708c"
const location = "8f383cca-1609-4326-8440-f75d943dc924"

type principalKey struct{}
type fixture struct {
	pool        *pgxpool.Pool
	location    rpc.LocationDisablementServiceClient
	requests    rpc.LocationAdditionRequestServiceClient
	now         atomic.Int64
	clockOffset time.Duration
	ctx         context.Context
}

func ptr[T any](v T) *T { return &v }
func req[T any](message *T, role string) *connect.Request[T] {
	r := connect.NewRequest(message)
	if role != "" {
		r.Header().Set("Authorization", "Bearer "+role)
	}
	return r
}
func code(t *testing.T, e error, want connect.Code) {
	t.Helper()
	if connect.CodeOf(e) != want {
		t.Fatalf("expected %v, got %v", want, e)
	}
}
func (f *fixture) time() time.Time {
	return time.UnixMicro(f.now.Load()).UTC().Add(f.clockOffset)
}
func (f *fixture) proposal() *pb.LocationInput {
	return &pb.LocationInput{Name: " Cafe ", IsSupplier: ptr(true), CategoryIds: []string{category, categoryTwo}, BuildingId: building, Floor: ptr(" B1 "), Coordinates: &pb.Coordinates{Latitude: 1.294, Longitude: 103.774}, OpensAt: &timeofday.TimeOfDay{Hours: 22}, ClosesAt: &timeofday.TimeOfDay{Hours: 2}, Contact: ptr(" contact "), Details: " directions "}
}
func (f *fixture) submit(t *testing.T, role string) *pb.LocationAdditionRequest {
	t.Helper()
	v, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(&pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: uuid.NewString()}, role))
	if e != nil {
		t.Fatal(e)
	}
	return v.Msg.Request
}
func (f *fixture) create(t *testing.T, starts, ends *time.Time) *pb.Disablement {
	t.Helper()
	input := &pb.CreateDisablementRequest{LocationId: location, Reason: "maintenance", IdempotencyKey: uuid.NewString()}
	if starts != nil {
		input.StartsAt = timestamppb.New(*starts)
	}
	if ends != nil {
		input.EndsAt = timestamppb.New(*ends)
	}
	r, e := f.location.CreateDisablement(f.ctx, req(input, "admin"))
	if e != nil {
		t.Fatal(e)
	}
	return r.Msg.Disablement
}

func TestOperationalWorkflowsPostGIS(t *testing.T) {
	f := newFixture(t)
	// Mutation responses must match PostgreSQL's microsecond snapshots even
	// when the application clock has finer precision.
	f.clockOffset = 417 * time.Nanosecond
	if f.time().Nanosecond()%1000 == 0 {
		t.Fatal("clock must retain sub-microsecond precision")
	}
	t.Run("scheduled updates cancellation and adjacent intervals", func(t *testing.T) {
		f.reset(t)
		start := f.time().Add(time.Hour)
		end := start.Add(time.Hour)
		d := f.create(t, &start, &end)
		f.now.Add(int64(time.Second / time.Microsecond))
		input := &pb.UpdateDisablementRequest{Id: d.Id, Reason: "changed", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"reason"}}, ExpectedRevision: 1}
		updated, e := f.location.UpdateDisablement(f.ctx, req(input, "admin"))
		if e != nil || updated.Msg.Disablement.Revision != 2 {
			t.Fatalf("update: %v %v", updated, e)
		}
		persisted, e := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location}, "admin"))
		if e != nil || len(persisted.Msg.Disablements) != 1 || !proto.Equal(updated.Msg.Disablement, persisted.Msg.Disablements[0]) {
			t.Fatalf("Update versus persisted list: %v err=%v", persisted, e)
		}
		_, e = f.location.UpdateDisablement(f.ctx, req(input, "admin"))
		code(t, e, connect.CodeAborted)
		_, e = f.location.CreateDisablement(f.ctx, req(&pb.CreateDisablementRequest{LocationId: location, StartsAt: timestamppb.New(start), Reason: "overlap", IdempotencyKey: uuid.NewString()}, "admin"))
		code(t, e, connect.CodeAlreadyExists)
		adjacent := f.create(t, &end, nil)
		if adjacent.State != pb.DisablementState_DISABLEMENT_STATE_SCHEDULED {
			t.Fatal("adjacent interval is not scheduled")
		}
		cancelled, e := f.location.CancelDisablement(f.ctx, req(&pb.CancelDisablementRequest{Id: d.Id}, "admin"))
		if e != nil || cancelled.Msg.Disablement.CancelledAt == nil {
			t.Fatalf("cancel: %v %v", cancelled, e)
		}
		again, e := f.location.CancelDisablement(f.ctx, req(&pb.CancelDisablementRequest{Id: d.Id}, "super_admin"))
		if e != nil || !proto.Equal(cancelled.Msg, again.Msg) {
			t.Fatalf("cancel retry: %v %v", again, e)
		}
		_, e = f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: d.Id}, "admin"))
		code(t, e, connect.CodeFailedPrecondition)
		list, e := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location, State: pb.DisablementState_DISABLEMENT_STATE_CANCELLED}, "admin"))
		if e != nil || len(list.Msg.Disablements) != 1 || list.Msg.PageInfo.TotalItems != 1 || !proto.Equal(cancelled.Msg.Disablement, list.Msg.Disablements[0]) {
			t.Fatalf("cancelled list: %v %v", list, e)
		}
	})
	t.Run("immediate creation idempotency early end and current warning", func(t *testing.T) {
		f.reset(t)
		input := &pb.CreateDisablementRequest{LocationId: location, Reason: "closure", IdempotencyKey: uuid.NewString()}
		first, e := f.location.CreateDisablement(f.ctx, req(input, "admin"))
		if e != nil {
			t.Fatal(e)
		}
		f.now.Add(int64(time.Hour / time.Microsecond))
		retry, e := f.location.CreateDisablement(f.ctx, req(input, "admin"))
		if e != nil || !proto.Equal(first.Msg, retry.Msg) {
			t.Fatalf("retry: %v %v", retry, e)
		}
		input.Reason = "different"
		_, e = f.location.CreateDisablement(f.ctx, req(input, "admin"))
		code(t, e, connect.CodeAlreadyExists)
		// Observe the shared Location result through the repository boundary.
		real := additionrequest.NewPostgresRepository(f.pool, f.time)
		e = real.Within(f.ctx, func(tx additionrequest.Tx) error {
			l, e := tx.Location(f.ctx, location)
			if e == nil && (l.CurrentDisablement == nil || l.ArchivedAt != nil) {
				t.Error("disabled Location lost its selectable warning representation")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		ended, e := f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: first.Msg.Disablement.Id}, "admin"))
		if e != nil || ended.Msg.Disablement.EndedAt == nil {
			t.Fatalf("end: %v %v", ended, e)
		}
		again, e := f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: first.Msg.Disablement.Id}, "admin"))
		if e != nil || !proto.Equal(ended.Msg, again.Msg) {
			t.Fatalf("end retry: %v %v", again, e)
		}
		persisted, e := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location}, "admin"))
		if e != nil || len(persisted.Msg.Disablements) != 1 || !proto.Equal(ended.Msg.Disablement, persisted.Msg.Disablements[0]) {
			t.Fatalf("End versus persisted list: %v err=%v", persisted, e)
		}
		_, e = f.location.CancelDisablement(f.ctx, req(&pb.CancelDisablementRequest{Id: first.Msg.Disablement.Id}, "admin"))
		code(t, e, connect.CodeFailedPrecondition)
		f.create(t, nil, nil)
	})
	t.Run("natural end and expired idempotency", func(t *testing.T) {
		f.reset(t)
		end := f.time().Add(time.Hour)
		d := f.create(t, nil, &end)
		f.now.Add(int64(24 * time.Hour / time.Microsecond))
		ended, e := f.location.EndDisablement(f.ctx, req(&pb.EndDisablementRequest{Id: d.Id}, "admin"))
		if e != nil || ended.Msg.Disablement.State != pb.DisablementState_DISABLEMENT_STATE_ENDED || ended.Msg.Disablement.EndedAt != nil {
			t.Fatalf("natural end retry: %v %v", ended, e)
		}
		input := &pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: uuid.NewString()}
		first, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(input, "owner"))
		if e != nil {
			t.Fatal(e)
		}
		f.now.Add(int64(24 * time.Hour / time.Microsecond))
		input.Proposal.Name = "new after expiry"
		next, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(input, "owner"))
		if e != nil || next.Msg.Request.Id == first.Msg.Request.Id {
			t.Fatalf("expiry: %v %v", next, e)
		}
	})
	t.Run("approval atomically creates multi Category Location and redacts public responses", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		_, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "other"))
		code(t, e, connect.CodeNotFound)
		f.now.Add(int64(time.Second / time.Microsecond))
		result, e := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
		if e != nil {
			t.Fatal(e)
		}
		l := result.Msg.Location
		if l.Name != "Cafe" || l.Floor == nil || *l.Floor != "B1" || len(l.Categories) != 2 || l.Building.Id != building || l.OpensAt.Hours != 22 || l.ClosesAt.Hours != 2 || l.Coordinates.Latitude != 1.294 {
			t.Fatalf("Location: %+v", l)
		}
		retry, e := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "super_admin"))
		if e != nil || !proto.Equal(result.Msg, retry.Msg) {
			t.Fatalf("approval retry: %v %v", retry, e)
		}
		for _, role := range []string{"other", "suspended_user"} {
			public, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, role))
			if e != nil || public.Msg.Request.SubmittedBy != "" || public.Msg.Request.ReviewedBy != nil || public.Msg.Request.ReviewNote != nil {
				t.Fatalf("public response %s: %v %v", role, public, e)
			}
		}
		for _, role := range []string{"owner", "admin", "super_admin"} {
			full, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, role))
			if e != nil || full.Msg.Request.SubmittedBy != "owner" || full.Msg.Request.ReviewedBy == nil {
				t.Fatalf("full response %s: %v %v", role, full, e)
			}
		}
		list, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{Status: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED}, "other"))
		if e != nil || list.Msg.PageInfo.TotalItems != 1 || list.Msg.Requests[0].SubmittedBy != "" {
			t.Fatalf("public list: %v %v", list, e)
		}
		_, e = f.requests.RejectLocationAdditionRequest(f.ctx, req(&pb.RejectLocationAdditionRequestRequest{Id: r.Id, ReviewNote: "different transition"}, "admin"))
		code(t, e, connect.CodeFailedPrecondition)
	})
	t.Run("pending patch clear optimistic revision withdrawal and rejection", func(t *testing.T) {
		f.reset(t)
		submit := &pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: uuid.NewString()}
		first, err := f.requests.SubmitLocationAdditionRequest(f.ctx, req(submit, "owner"))
		if err != nil {
			t.Fatal(err)
		}
		replay, err := f.requests.SubmitLocationAdditionRequest(f.ctx, req(submit, "owner"))
		if err != nil || !proto.Equal(first.Msg, replay.Msg) {
			t.Fatalf("Submit versus replay: %v err=%v", replay, err)
		}
		r := first.Msg.Request

		// Invalid new input and legacy masks must leave the pending request untouched.
		for _, edit := range []func(*pb.UpdateLocationAdditionRequestRequest){
			func(q *pb.UpdateLocationAdditionRequestRequest) { q.Proposal.OpensAt.Seconds = 1 },
			func(q *pb.UpdateLocationAdditionRequestRequest) { q.Proposal.ClosesAt.Nanos = 1000 },
			func(q *pb.UpdateLocationAdditionRequestRequest) { q.Proposal.OpensAt.Minutes = 60 },
			func(q *pb.UpdateLocationAdditionRequestRequest) {
				q.UpdateMask.Paths = []string{"open_from", "open_to"}
			},
			func(q *pb.UpdateLocationAdditionRequestRequest) { q.Proposal = nil },
		} {
			q := &pb.UpdateLocationAdditionRequestRequest{Id: r.Id, Proposal: &pb.LocationInput{OpensAt: &timeofday.TimeOfDay{Hours: 23, Minutes: 30}, ClosesAt: &timeofday.TimeOfDay{Hours: 3}}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"opens_at", "closes_at"}}, ExpectedRevision: 1}
			edit(q)
			_, err := f.requests.UpdateLocationAdditionRequest(f.ctx, req(q, "owner"))
			code(t, err, connect.CodeInvalidArgument)
		}
		current, err := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if err != nil || current.Msg.Request.Revision != 1 || current.Msg.Request.Proposal.OpensAt.Hours != 22 {
			t.Fatalf("invalid patch changed request: %v %v", current, err)
		}
		input := &pb.UpdateLocationAdditionRequestRequest{Id: r.Id, Proposal: &pb.LocationInput{Details: "updated"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"details", "floor", "contact", "opens_at", "closes_at"}}, ExpectedRevision: 1}
		f.now.Add(int64(time.Second / time.Microsecond))
		update, e := f.requests.UpdateLocationAdditionRequest(f.ctx, req(input, "owner"))
		if e != nil {
			t.Fatal(e)
		}
		p := update.Msg.Request.Proposal
		if p.Details != "updated" || p.Floor != nil || p.Contact != nil || p.OpensAt != nil || p.Name != "Cafe" {
			t.Fatalf("patch: %+v", p)
		}
		persisted, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if e != nil || !proto.Equal(update.Msg.Request, persisted.Msg.Request) {
			t.Fatalf("request Update versus read: %v err=%v", persisted, e)
		}
		_, e = f.requests.UpdateLocationAdditionRequest(f.ctx, req(input, "admin"))
		code(t, e, connect.CodeAborted)
		_, e = f.requests.UpdateLocationAdditionRequest(f.ctx, req(input, "other"))
		code(t, e, connect.CodeNotFound)
		f.now.Add(int64(time.Second / time.Microsecond))
		withdrawn, e := f.requests.WithdrawLocationAdditionRequest(f.ctx, req(&pb.WithdrawLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if e != nil || withdrawn.Msg.Request.Status != pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN {
			t.Fatalf("withdraw: %v %v", withdrawn, e)
		}
		again, e := f.requests.WithdrawLocationAdditionRequest(f.ctx, req(&pb.WithdrawLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if e != nil || !proto.Equal(withdrawn.Msg, again.Msg) {
			t.Fatalf("withdraw retry: %v %v", again, e)
		}
		_, e = f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
		code(t, e, connect.CodeFailedPrecondition)
		r = f.submit(t, "owner")
		f.now.Add(int64(time.Second / time.Microsecond))
		rejected, e := f.requests.RejectLocationAdditionRequest(f.ctx, req(&pb.RejectLocationAdditionRequestRequest{Id: r.Id, ReviewNote: " not suitable "}, "admin"))
		if e != nil || *rejected.Msg.Request.ReviewNote != "not suitable" {
			t.Fatalf("reject: %v %v", rejected, e)
		}
		rejectRetry, e := f.requests.RejectLocationAdditionRequest(f.ctx, req(&pb.RejectLocationAdditionRequestRequest{Id: r.Id, ReviewNote: "new note"}, "admin"))
		if e != nil || !proto.Equal(rejected.Msg, rejectRetry.Msg) {
			t.Fatalf("reject retry: %v %v", rejectRetry, e)
		}
		_, e = f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "other"))
		code(t, e, connect.CodeNotFound)
	})

	t.Run("shared input partial overnight hours persist through approval", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		patched, err := f.requests.UpdateLocationAdditionRequest(f.ctx, req(&pb.UpdateLocationAdditionRequestRequest{Id: r.Id, Proposal: &pb.LocationInput{OpensAt: &timeofday.TimeOfDay{Hours: 23, Minutes: 30}, ClosesAt: &timeofday.TimeOfDay{Hours: 3, Minutes: 15}}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"opens_at", "closes_at"}}, ExpectedRevision: 1}, "owner"))
		if err != nil || patched.Msg.Request.Proposal.Name != "Cafe" || patched.Msg.Request.Revision != 2 {
			t.Fatalf("partial shared input: %v %v", patched, err)
		}
		approved, err := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
		if err != nil || approved.Msg.Location.OpensAt.Hours != 23 || approved.Msg.Location.OpensAt.Minutes != 30 || approved.Msg.Location.ClosesAt.Minutes != 15 {
			t.Fatalf("approved shared hours: %v %v", approved, err)
		}
	})
	t.Run("pagination counts visibility and extreme pages", func(t *testing.T) {
		f.reset(t)
		for i := 0; i < 3; i++ {
			f.submit(t, "owner")
		}
		f.submit(t, "other")
		page, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{PageSize: 2}, "owner"))
		if e != nil || len(page.Msg.Requests) != 2 || page.Msg.PageInfo.TotalItems != 3 || page.Msg.PageInfo.TotalPages != 2 {
			t.Fatalf("page 1: %v %v", page, e)
		}
		second, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{Page: 2, PageSize: 2}, "owner"))
		if e != nil || len(second.Msg.Requests) != 1 || second.Msg.Requests[0].Id == page.Msg.Requests[0].Id || second.Msg.Requests[0].Id == page.Msg.Requests[1].Id {
			t.Fatalf("page 2: %v %v", second, e)
		}
		far, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{Page: 2147483647, PageSize: 100}, "admin"))
		if e != nil || len(far.Msg.Requests) != 0 || far.Msg.PageInfo.TotalItems != 4 {
			t.Fatalf("far page: %v %v", far, e)
		}
	})
	t.Run("all roles and invalid transport input", func(t *testing.T) {
		f.reset(t)
		for _, role := range []string{"", "owner", "suspended_user", "admin", "super_admin"} {
			_, e := f.location.ListDisablements(f.ctx, req(&pb.ListDisablementsRequest{LocationId: location}, role))
			if role == "" {
				code(t, e, connect.CodeUnauthenticated)
			} else if role == "owner" || role == "suspended_user" {
				code(t, e, connect.CodePermissionDenied)
			} else if e != nil {
				t.Fatal(e)
			}
			_, e = f.requests.SubmitLocationAdditionRequest(f.ctx, req(&pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: uuid.NewString()}, role))
			if role == "" {
				code(t, e, connect.CodeUnauthenticated)
			} else if role == "suspended_user" {
				code(t, e, connect.CodePermissionDenied)
			} else if e != nil {
				t.Fatal(e)
			}
		}
		for _, modify := range []func(*pb.LocationInput){func(p *pb.LocationInput) { p.Coordinates = nil }, func(p *pb.LocationInput) { p.IsSupplier = nil }, func(p *pb.LocationInput) { p.Coordinates.Latitude = 91 }, func(p *pb.LocationInput) { p.OpensAt.Hours = 24 }, func(p *pb.LocationInput) { p.OpensAt.Nanos = 1 }, func(p *pb.LocationInput) { p.ClosesAt = nil }, func(p *pb.LocationInput) { p.Name = strings.Repeat("x", 201) }} {
			p := f.proposal()
			modify(p)
			_, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(&pb.SubmitLocationAdditionRequestRequest{Proposal: p, IdempotencyKey: uuid.NewString()}, "owner"))
			code(t, e, connect.CodeInvalidArgument)
		}
		_, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{Status: pb.LocationAdditionRequestStatus(99)}, "owner"))
		code(t, e, connect.CodeInvalidArgument)
		_, e = f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: uuid.NewString()}, "owner"))
		code(t, e, connect.CodeNotFound)
		execute(t, f.pool, "UPDATE locations SET archived_at=now() WHERE id=$1", location)
		_, e = f.location.CreateDisablement(f.ctx, req(&pb.CreateDisablementRequest{LocationId: location, Reason: "closure", IdempotencyKey: uuid.NewString()}, "admin"))
		code(t, e, connect.CodeFailedPrecondition)
	})

	t.Run("trimmed boundary values pass declarative validation and persistence", func(t *testing.T) {
		f.reset(t)
		p := f.proposal()
		p.Name = " " + strings.Repeat("x", 200) + " "
		floor := " " + strings.Repeat("x", 50) + " "
		contact := " " + strings.Repeat("x", 500) + " "
		p.Floor = &floor
		p.Contact = &contact
		p.Details = " " + strings.Repeat("x", 2000) + " "
		r, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(&pb.SubmitLocationAdditionRequestRequest{Proposal: p, IdempotencyKey: uuid.NewString()}, "owner"))
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Msg.Request.Proposal.Name) != 200 || len(*r.Msg.Request.Proposal.Floor) != 50 || len(*r.Msg.Request.Proposal.Contact) != 500 || len(r.Msg.Request.Proposal.Details) != 2000 {
			t.Fatalf("text not normalized: %+v", r)
		}
		key := uuid.NewString()
		d, e := f.location.CreateDisablement(f.ctx, req(&pb.CreateDisablementRequest{LocationId: location, Reason: " " + strings.Repeat("x", 500) + " ", IdempotencyKey: key}, "admin"))
		if e != nil {
			t.Fatal(e)
		}
		retry, e := f.location.CreateDisablement(f.ctx, req(&pb.CreateDisablementRequest{LocationId: strings.ToUpper(location), Reason: strings.Repeat("x", 500), IdempotencyKey: strings.ToUpper(key)}, "admin"))
		if e != nil || retry.Msg.Disablement.Id != d.Msg.Disablement.Id {
			t.Fatalf("normalized UUID retry: %v %v", retry, e)
		}
	})

	t.Run("concurrent identical idempotency keys and interval creation", func(t *testing.T) {
		f.reset(t)
		key := uuid.NewString()
		results := make(chan *pb.LocationAdditionRequest, 8)
		errs := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			retryKey := key
			if i%2 == 1 {
				retryKey = strings.ToUpper(key)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, e := f.requests.SubmitLocationAdditionRequest(f.ctx, req(&pb.SubmitLocationAdditionRequestRequest{Proposal: f.proposal(), IdempotencyKey: retryKey}, "owner"))
				errs <- e
				if e == nil {
					results <- v.Msg.Request
				}
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		var first string
		for v := range results {
			if first == "" {
				first = v.Id
			} else if first != v.Id {
				t.Fatal("duplicate idempotent resources")
			}
		}
		list, e := f.requests.ListLocationAdditionRequests(f.ctx, req(&pb.ListLocationAdditionRequestsRequest{}, "admin"))
		if e != nil || list.Msg.PageInfo.TotalItems != 1 {
			t.Fatalf("idempotent count: %v %v", list, e)
		}
		codes := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				_, e := f.location.CreateDisablement(f.ctx, req(&pb.CreateDisablementRequest{LocationId: location, Reason: "closure", IdempotencyKey: uuid.NewString()}, "admin"))
				codes <- e
			}()
		}
		a, b := <-codes, <-codes
		if (a == nil) == (b == nil) {
			t.Fatalf("overlap race: %v %v", a, b)
		}
		if a != nil {
			code(t, a, connect.CodeAlreadyExists)
		}
		if b != nil {
			code(t, b, connect.CodeAlreadyExists)
		}
	})
	t.Run("approval rollback when category insertion fails", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		execute(t, f.pool, `CREATE FUNCTION fail_category() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected transaction failure'; END $$; CREATE TRIGGER fail_category BEFORE INSERT ON location_categories FOR EACH ROW EXECUTE FUNCTION fail_category()`)
		_, e := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
		code(t, e, connect.CodeInternal)
		if strings.Contains(e.Error(), "injected") {
			t.Fatal("dependency cause exposed")
		}
		execute(t, f.pool, "DROP TRIGGER fail_category ON location_categories; DROP FUNCTION fail_category()")
		pending, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "owner"))
		if e != nil || pending.Msg.Request.Status != pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING || pending.Msg.Request.ResultingLocationId != nil {
			t.Fatalf("rollback request: %v %v", pending, e)
		}
		// Re-approve with an ID-collision guard: a leaked Location from the failed
		// transaction would conflict here, so this verifies rollback at the API seam.
		execute(t, f.pool, "CREATE UNIQUE INDEX test_location_name ON locations(name)")
		result, e := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
		if e != nil || len(result.Msg.Location.Categories) != 2 {
			t.Fatalf("approval after rollback: %v %v", result, e)
		}
		execute(t, f.pool, "DROP INDEX test_location_name")
	})
	t.Run("blocked owner writes recheck terminal state after approval", func(t *testing.T) {
		f.reset(t)
		for _, operation := range []string{"update", "withdraw"} {
			r := f.submit(t, "owner")
			tx, e := f.pool.Begin(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(context.WithoutCancel(f.ctx))
			if _, e = tx.Exec(f.ctx, "SELECT id FROM location_addition_requests WHERE id=$1 FOR UPDATE", r.Id); e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() {
				if operation == "update" {
					_, e := f.requests.UpdateLocationAdditionRequest(f.ctx, req(&pb.UpdateLocationAdditionRequestRequest{Id: r.Id, Proposal: &pb.LocationInput{Name: "should not persist"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}}, ExpectedRevision: 1}, "owner"))
					result <- e
				} else {
					_, e := f.requests.WithdrawLocationAdditionRequest(f.ctx, req(&pb.WithdrawLocationAdditionRequestRequest{Id: r.Id}, "owner"))
					result <- e
				}
			}()
			waitForBlockedQuery(t, f.pool)
			// Hold the same lock as approval and perform its pending-to-terminal SQL.
			// The real endpoint approval path is already exercised above.
			created := uuid.NewString()
			if _, e = tx.Exec(f.ctx, "INSERT INTO locations(id,name,building_id,coordinates) VALUES($1,'race winner',$2,ST_SetSRID(ST_MakePoint(103.77,1.29),4326)::geography)", created, building); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(f.ctx, "UPDATE location_addition_requests SET status='approved',reviewed_by='admin',reviewed_at=now(),resulting_location_id=$2,revision=revision+1 WHERE id=$1", r.Id, created); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(f.ctx); e != nil {
				t.Fatal(e)
			}
			code(t, <-result, connect.CodeFailedPrecondition)
			full, e := f.requests.GetLocationAdditionRequest(f.ctx, req(&pb.GetLocationAdditionRequestRequest{Id: r.Id}, "owner"))
			if e != nil || full.Msg.Request.Proposal.Name != "Cafe" || full.Msg.Request.Status != pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED {
				t.Fatalf("terminal request modified: %v %v", full, e)
			}
		}
	})

	t.Run("approval waiting behind a Category edit uses committed Category links", func(t *testing.T) {
		f.reset(t)
		r := f.submit(t, "owner")
		tx, e := f.pool.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(f.ctx)
		if _, e = tx.Exec(f.ctx, "UPDATE location_addition_requests SET name='Changed Cafe',revision=revision+1 WHERE id=$1", r.Id); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(f.ctx, "DELETE FROM location_addition_request_categories WHERE request_id=$1", r.Id); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(f.ctx, "INSERT INTO location_addition_request_categories(request_id,category_id) VALUES($1,$2)", r.Id, categoryTwo); e != nil {
			t.Fatal(e)
		}
		result := make(chan *pb.ApproveLocationAdditionRequestResponse, 1)
		errs := make(chan error, 1)
		go func() {
			v, e := f.requests.ApproveLocationAdditionRequest(f.ctx, req(&pb.ApproveLocationAdditionRequestRequest{Id: r.Id}, "admin"))
			errs <- e
			if e == nil {
				result <- v.Msg
			}
		}()
		waitForBlockedQuery(t, f.pool)
		if e = tx.Commit(f.ctx); e != nil {
			t.Fatal(e)
		}
		if e = <-errs; e != nil {
			t.Fatal(e)
		}
		v := <-result
		if v.Location.Name != "Changed Cafe" || len(v.Location.Categories) != 1 || v.Location.Categories[0].Id != categoryTwo || len(v.Request.Proposal.CategoryIds) != 1 || v.Request.Proposal.CategoryIds[0] != categoryTwo {
			t.Fatalf("approval used stale proposal: %+v", v)
		}
	})

}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	options := []testcontainers.ContainerCustomizer{postgres.WithDatabase("supplier_test"), postgres.WithUsername("supplier"), postgres.WithPassword("supplier"), postgres.BasicWaitStrategies()}
	if runtime.GOARCH == "arm64" {
		options = append(options, testcontainers.WithImagePlatform("linux/amd64"))
	}
	container, e := postgres.Run(ctx, "postgis/postgis:18-3.6", options...)
	if e != nil {
		t.Fatal(e)
	}
	testcontainers.CleanupContainer(t, container)
	url, e := container.ConnectionString(ctx, "sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("pgx", url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, file, _, _ := runtime.Caller(0)
	if e = goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e = goose.Up(db, filepath.Join(filepath.Dir(file), "../../../database/schema")); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	f := &fixture{pool: pool, ctx: context.Background()}
	f.now.Store(time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC).UnixMicro())
	operations := newTestDomainServices(pool, f.time)
	// This tests the authenticated mounting seam, not the production JWT
	// verifier. Only fixture-owned tokens produce a verified test Principal.
	principal := func(ctx context.Context) additionrequest.Caller {
		c, _ := ctx.Value(principalKey{}).(additionrequest.Caller)
		return c
	}
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			c := additionrequest.Caller{}
			switch token {
			case "owner", "other":
				c = additionrequest.Caller{ID: token, Role: "user"}
			case "admin", "super_admin", "suspended_user":
				c = additionrequest.Caller{ID: token, Role: token}
			}
			next.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), principalKey{}, c)))
		})
	}
	mux := http.NewServeMux()
	if e = mountTestServices(mux, newTestServers(operations, principal, f.time), authenticate); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	f.location = rpc.NewLocationDisablementServiceClient(server.Client(), server.URL)
	f.requests = rpc.NewLocationAdditionRequestServiceClient(server.Client(), server.URL)
	return f
}
func (f *fixture) reset(t *testing.T) {
	t.Helper()
	f.now.Store(time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC).UnixMicro())
	execute(t, f.pool, "TRUNCATE supplier_idempotency, location_addition_requests, locations, buildings, categories CASCADE")
	execute(t, f.pool, "INSERT INTO buildings(id,name,center,radius_m) VALUES($1,'COM2',ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography,75)", building)
	execute(t, f.pool, "INSERT INTO categories(id,name) VALUES($1,'Food'),($2,'Coffee')", category, categoryTwo)
	execute(t, f.pool, "INSERT INTO locations(id,name,building_id,coordinates) VALUES($1,'Existing Location',$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography)", location, building)
}
func execute(t *testing.T, pool *pgxpool.Pool, s string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(context.Background(), s, args...); e != nil {
		t.Fatal(e)
	}
}
func waitForBlockedQuery(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		e := pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%location_addition_requests%')").Scan(&blocked)
		if e != nil {
			t.Fatal(e)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owner write did not reach the row lock")
}
