package orders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

type participantRPC struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	response *orderv1.GetOrderParticipantsResponse
	err      error
}

func (s *participantRPC) GetOrderParticipants(
	context.Context,
	*connect.Request[orderv1.GetOrderParticipantsRequest],
) (*connect.Response[orderv1.GetOrderParticipantsResponse], error) {
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(s.response), nil
}

func gatewayFor(t *testing.T, stub *participantRPC) *ConnectGateway {
	t.Helper()
	path, handler := orderv1connect.NewOrderInternalServiceHandler(stub)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewConnectGateway(orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL))
}

func TestConnectGatewayReadsParticipants(t *testing.T) {
	orderID, requester, courier := uuid.New(), uuid.New(), uuid.New()
	courierText := courier.String()
	gateway := gatewayFor(t, &participantRPC{response: &orderv1.GetOrderParticipantsResponse{
		OrderId:     orderID.String(),
		RequesterId: requester.String(),
		CourierId:   &courierText,
		Status:      orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
	}})

	got, err := gateway.GetOrderParticipants(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Participants{RequesterID: requester, CourierID: courier, Completed: true}) {
		t.Fatalf("participants = %+v", got)
	}
}

func TestConnectGatewayFailsClosed(t *testing.T) {
	orderID := uuid.New()
	requester, courier := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name string
		stub *participantRPC
		want error
	}{
		{"unknown order", &participantRPC{err: connect.NewError(connect.CodeNotFound, nil)}, ErrNotFound},
		{"order unavailable", &participantRPC{err: connect.NewError(connect.CodeUnavailable, nil)}, ErrUnavailable},
		{"wrong order", &participantRPC{response: &orderv1.GetOrderParticipantsResponse{
			OrderId: uuid.NewString(), RequesterId: requester, CourierId: &courier,
		}}, ErrUnavailable},
		{"missing order", &participantRPC{response: &orderv1.GetOrderParticipantsResponse{
			RequesterId: requester, CourierId: &courier,
		}}, ErrUnavailable},
		{"zero requester", &participantRPC{response: &orderv1.GetOrderParticipantsResponse{
			OrderId: orderID.String(), RequesterId: uuid.Nil.String(), CourierId: &courier,
		}}, ErrUnavailable},
		{"malformed requester", &participantRPC{response: &orderv1.GetOrderParticipantsResponse{
			OrderId: orderID.String(), RequesterId: "bad", CourierId: &courier,
		}}, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gatewayFor(t, tc.stub).GetOrderParticipants(context.Background(), orderID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := (UnavailableGateway{}).GetOrderParticipants(context.Background(), orderID); !errors.Is(
		err, ErrUnavailable,
	) {
		t.Fatalf("unconfigured gateway error = %v", err)
	}
}

func TestParticipants(t *testing.T) {
	requester, courier, stranger := uuid.New(), uuid.New(), uuid.New()
	assigned := Participants{RequesterID: requester, CourierID: courier}
	unassigned := Participants{RequesterID: requester}

	if !assigned.Contains(requester, courier) || !assigned.Contains(courier, requester) {
		t.Fatal("the requester and courier are a pair")
	}
	if assigned.Contains(requester, stranger) || assigned.Contains(requester, requester) {
		t.Fatal("a stranger or a repeated participant is not a pair")
	}
	if unassigned.Contains(requester, uuid.Nil) {
		t.Fatal("an order without a courier has no pair")
	}
	if !assigned.HasParticipant(courier) || !unassigned.HasParticipant(requester) ||
		assigned.HasParticipant(stranger) || unassigned.HasParticipant(uuid.Nil) {
		t.Fatal("participant membership is wrong")
	}
}

func TestExternalError(t *testing.T) {
	for err, status := range map[error]int{
		ErrNotFound:    http.StatusNotFound,
		ErrUnavailable: http.StatusServiceUnavailable,
	} {
		var external api.ExternalError
		if !errors.As(ExternalError(err), &external) || external.Code() != status {
			t.Fatalf("ExternalError(%v) = %v, want status %d", err, ExternalError(err), status)
		}
	}
}
