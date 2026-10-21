package additionrequest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

type reviewOperations struct {
	Operations
	calls    int
	revision int64
	note     string
}

func (o *reviewOperations) ApproveRequest(_ context.Context, _ app.Caller, _ string, revision int64) (app.Approval, error) {
	o.calls++
	o.revision = revision
	return app.Approval{}, app.ErrAborted
}
func (o *reviewOperations) RejectRequest(_ context.Context, _ app.Caller, _, note string, revision int64) (app.AdditionRequest, error) {
	o.calls++
	o.revision = revision
	o.note = note
	return app.AdditionRequest{}, app.ErrAborted
}
func TestDecisionRevisionValidationAndConflictMapping(t *testing.T) {
	for _, method := range []string{"approve", "reject"} {
		t.Run(method, func(t *testing.T) {
			operations := &reviewOperations{}
			handler := NewServer(operations, func(context.Context) app.Caller { return app.Caller{ID: "admin", Role: "admin"} }, time.Now)
			mux := http.NewServeMux()
			if err := Mount(mux, handler, func(h http.Handler) http.Handler { return h }); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			client := locationv1connect.NewLocationAdditionRequestServiceClient(server.Client(), server.URL)
			decide := func(revision int64) error {
				id := "11111111-1111-4111-8111-111111111111"
				if method == "approve" {
					_, err := client.ApproveLocationAdditionRequest(context.Background(), connect.NewRequest(&pb.ApproveLocationAdditionRequestRequest{Id: id, ExpectedRevision: revision}))
					return err
				}
				_, err := client.RejectLocationAdditionRequest(context.Background(), connect.NewRequest(&pb.RejectLocationAdditionRequestRequest{Id: id, ExpectedRevision: revision, ReviewNote: "  Not on campus  "}))
				return err
			}
			for _, revision := range []int64{0, -1} {
				if code := connect.CodeOf(decide(revision)); code != connect.CodeInvalidArgument {
					t.Fatalf("revision %d: got %v", revision, code)
				}
			}
			if operations.calls != 0 {
				t.Fatal("invalid revision reached domain operations")
			}
			const revision int64 = 9007199254740993
			if code := connect.CodeOf(decide(revision)); code != connect.CodeAborted {
				t.Fatalf("stale revision: got %v", code)
			}
			if operations.calls != 1 || operations.revision != revision {
				t.Fatalf("revision translation: %+v", operations)
			}
			if method == "reject" && operations.note != "Not on campus" {
				t.Fatalf("note not normalized: %q", operations.note)
			}
		})
	}
}
