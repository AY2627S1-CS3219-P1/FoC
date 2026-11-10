package order_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

type operationFacts struct{ owner, id string }

func (r operationFacts) AssignmentStatus(context.Context, string) (app.AssignmentAttempt, error) {
	return app.AssignmentAttempt{
		ID:        r.id,
		CallerID:  r.owner,
		Phase:     app.AssignmentRejected,
		Rejection: "insufficient_credits",
	}, nil
}
func (r operationFacts) CancellationStatus(context.Context, string) (app.Cancellation, error) {
	return app.Cancellation{ID: r.id, CallerID: r.owner, Status: app.CancellationSuperseded}, nil
}

func TestReadOnlyOperationRPCs(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	server := rpc.NewServer(nil, nil, nil).WithOperationStatus(&app.OperationStatus{Reader: operationFacts{owner, id}})
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller := r.Header.Get("X-Test-User"); caller != "" {
				r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: caller, Role: "suspended_user"}))
			}
			next.ServeHTTP(w, r)
		})
	}
	httpServer := httptest.NewServer(router.Setup(router.RPCServices{
		Order:  server,
		Health: &orderv1connect.UnimplementedHealthServiceHandler{},
		Admin:  &orderv1connect.UnimplementedOrderAdminServiceHandler{},
	}, authenticate))
	defer httpServer.Close()
	client := orderv1connect.NewOrderServiceClient(httpServer.Client(), httpServer.URL)
	for _, caller := range []string{"", owner, uuid.NewString()} {
		request := connect.NewRequest(&pb.GetAssignmentAttemptRequest{AttemptId: id})
		request.Header().Set("X-Test-User", caller)
		response, err := client.GetAssignmentAttempt(context.Background(), request)
		cancellation := connect.NewRequest(&pb.GetCancellationRequest{OperationId: id})
		cancellation.Header().Set("X-Test-User", caller)
		cancelled, cerr := client.GetCancellation(context.Background(), cancellation)
		if caller == owner {
			if err != nil || cerr != nil ||
				response.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_REJECTED ||
				cancelled.Msg.Cancellation.Status != pb.CancellationStatus_CANCELLATION_STATUS_SUPERSEDED {
				t.Fatal(response, cancelled, err, cerr)
			}
		} else {
			want := connect.CodeNotFound
			if caller == "" {
				want = connect.CodeUnauthenticated
			}
			if connect.CodeOf(err) != want || connect.CodeOf(cerr) != want {
				t.Fatal(caller, err, cerr)
			}
		}
	}
}
