package review

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/google/uuid"
)

type participantRPC struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	response  *orderv1.GetOrderParticipantsResponse
	err       error
	requested string
	lookup    func(string) (*orderv1.GetOrderParticipantsResponse, error)
}

func (s *participantRPC) GetOrderParticipants(
	_ context.Context,
	req *connect.Request[orderv1.GetOrderParticipantsRequest],
) (*connect.Response[orderv1.GetOrderParticipantsResponse], error) {
	s.requested = req.Msg.GetOrderId()
	if s.lookup != nil {
		response, err := s.lookup(s.requested)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(response), nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(s.response), nil
}

// Authenticated Review RPC -> domain checks -> Connect Order client.
func TestReviewEligibilityThroughOrderRPC(t *testing.T) {
	requester, courier, stranger, orderID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name            string
		actor, from, to uuid.UUID
		status          orderv1.OrderStatus
		courier         string
		rpcErr          error
		code            connect.Code
	}{
		{"requester to courier", requester, requester, courier, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, courier.String(), nil, 0},
		{"courier to requester", courier, courier, requester, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, courier.String(), nil, 0},
		{"stranger rates participant", stranger, stranger, courier, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, courier.String(), nil, connect.CodePermissionDenied},
		{"participant rates stranger", requester, requester, stranger, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, courier.String(), nil, connect.CodePermissionDenied},
		{"impersonation", stranger, requester, courier, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, courier.String(), nil, connect.CodePermissionDenied},
		{"incomplete", requester, requester, courier, orderv1.OrderStatus_ORDER_STATUS_DELIVERED, courier.String(), nil, connect.CodeInvalidArgument},
		{"unassigned", requester, requester, courier, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, "", nil, connect.CodePermissionDenied},
		{"unknown order", requester, requester, courier, 0, "", connect.NewError(connect.CodeNotFound, nil), connect.CodeNotFound},
		{"order unavailable", requester, requester, courier, 0, "", connect.NewError(connect.CodeUnavailable, nil), connect.CodeInternal},
		{"malformed response", requester, requester, courier, orderv1.OrderStatus_ORDER_STATUS_COMPLETED, "bad", nil, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &participantRPC{
				response: &orderv1.GetOrderParticipantsResponse{
					OrderId:     orderID.String(),
					RequesterId: requester.String(),
					Status:      tc.status,
				},
				err: tc.rpcErr,
			}
			if tc.courier != "" {
				stub.response.CourierId = &tc.courier
			}
			path, h := orderv1connect.NewOrderInternalServiceHandler(stub)
			mux := http.NewServeMux()
			mux.Handle(path, h)
			server := httptest.NewServer(mux)
			defer server.Close()
			repo := &fakeRepository{exists: map[uuid.UUID]bool{requester: true, courier: true, stranger: true}}
			svc := newTestService(
				repo,
				NewConnectOrderGateway(orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)),
			)
			client := reviewClient(t, svc, auth.Caller{ID: tc.actor.String()})
			_, e := client.SubmitReview(
				context.Background(),
				connect.NewRequest(
					&reviewv1.SubmitReviewRequest{
						From:       tc.from.String(),
						To:         tc.to.String(),
						OrderId:    orderID.String(),
						Submission: &reviewv1.ReviewSubmission{Rating: 5, Comment: "ok"},
					},
				),
			)
			if tc.code == 0 {
				if e != nil || repo.insertedID == uuid.Nil || repo.recalcCount != 1 {
					t.Fatalf("valid review: %v", e)
				}
			} else if connect.CodeOf(e) != tc.code || repo.insertedID != uuid.Nil || repo.recalcCount != 0 {
				t.Fatalf("rejected review: %v writes=%v", e, repo.insertedID)
			}
			if tc.name != "impersonation" && stub.requested != orderID.String() {
				t.Fatal("wrong order lookup")
			}
		})
	}
}

func TestOrderGatewayRejectsInvalidResponseIdentity(t *testing.T) {
	orderID, requester, courier := uuid.New(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct{ name, id, requester, courier string }{
		{"wrong order", uuid.NewString(), requester, courier},
		{"missing order", "", requester, courier},
		{"zero requester", orderID.String(), uuid.Nil.String(), courier},
		{"malformed requester", orderID.String(), "bad", courier},
		{"zero courier", orderID.String(), requester, uuid.Nil.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &participantRPC{
				response: &orderv1.GetOrderParticipantsResponse{
					OrderId:     tc.id,
					RequesterId: tc.requester,
					CourierId:   &tc.courier,
					Status:      orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
				},
			}
			path, h := orderv1connect.NewOrderInternalServiceHandler(stub)
			mux := http.NewServeMux()
			mux.Handle(path, h)
			server := httptest.NewServer(mux)
			defer server.Close()
			gateway := NewConnectOrderGateway(orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL))
			if _, err := gateway.GetOrderParticipants(
				context.Background(),
				orderID,
			); !errors.Is(
				err,
				errOrderUnavailable,
			) {
				t.Fatalf("invalid response accepted: %v", err)
			}
		})
	}
}

func orderGatewayForIntegration(t *testing.T, orders *stubOrders) OrderGateway {
	t.Helper()
	stub := &participantRPC{lookup: func(id string) (*orderv1.GetOrderParticipantsResponse, error) {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		p, err := orders.GetOrderParticipants(context.Background(), parsed)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		response := &orderv1.GetOrderParticipantsResponse{
			OrderId:     id,
			RequesterId: p.RequesterID.String(),
			Status:      orderv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		}
		if p.CourierID != uuid.Nil {
			courier := p.CourierID.String()
			response.CourierId = &courier
		}
		if p.Completed {
			response.Status = orderv1.OrderStatus_ORDER_STATUS_COMPLETED
		}
		return response, nil
	}}
	path, handler := orderv1connect.NewOrderInternalServiceHandler(stub)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewConnectOrderGateway(orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL))
}
