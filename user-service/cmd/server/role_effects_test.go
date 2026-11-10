package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type suspensionAcknowledgment struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	calls int
}

func (s *suspensionAcknowledgment) ApplyAccountSuspension(
	_ context.Context,
	req *connect.Request[pb.ApplyAccountSuspensionRequest],
) (*connect.Response[pb.ApplyAccountSuspensionResponse], error) {
	if req.Header().Get("Authorization") != "" || req.Header().Get("Cookie") != "" {
		return nil, errors.New("human credentials reached workload transport")
	}
	s.calls++
	return connect.NewResponse(&pb.ApplyAccountSuspensionResponse{DecisionId: req.Msg.DecisionId}), nil
}

// Source: durable suspension retries must work without retaining the admin JWT,
// and the private client must not follow a redirect outside its workload target.
func TestOrderRoleTransportUsesNoHumanCredentialsOrRedirects(t *testing.T) {
	if newOrderRoleEffects("") != nil {
		t.Fatal("missing origin enabled suspension")
	}
	acknowledgment := &suspensionAcknowledgment{}
	_, handler := orderv1connect.NewOrderInternalServiceHandler(acknowledgment)
	server := httptest.NewServer(handler)
	defer server.Close()
	dispatch := models.RoleDispatch{
		ID:       uuid.New(),
		UserID:   uuid.New(),
		AdminID:  uuid.New(),
		Revision: 1,
		ToRole:   models.RoleSuspended,
		Reason:   "explicit decision",
	}
	ctx := auth.WithUserBearer(context.Background(), "Bearer expired-admin-session")
	for range 2 {
		if err := newOrderRoleEffects(server.URL).ApplyRoleDecision(ctx, dispatch); err != nil {
			t.Fatal(err)
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if err := newOrderRoleEffects(
		redirect.URL,
	).ApplyRoleDecision(ctx, dispatch); err == nil ||
		acknowledgment.calls != 2 {
		t.Fatalf("redirect followed: err=%v calls=%d", err, acknowledgment.calls)
	}
}
