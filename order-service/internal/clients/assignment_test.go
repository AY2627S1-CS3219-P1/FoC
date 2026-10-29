package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

type assignmentProbe struct {
	creditv1connect.UnimplementedCreditReservationServiceHandler
	reservation *pb.Reservation
	adjustment  *pb.AdjustReservationRequest
	revert      *pb.RevertAdjustmentRequest
	refusal     error
	bearers     []string
}

func (p *assignmentProbe) AdjustReservation(
	_ context.Context,
	req *connect.Request[pb.AdjustReservationRequest],
) (*connect.Response[pb.AdjustReservationResponse], error) {
	p.bearers = append(p.bearers, req.Header().Get("Authorization"))
	p.adjustment = req.Msg
	if p.refusal != nil {
		return nil, p.refusal
	}
	return connect.NewResponse(&pb.AdjustReservationResponse{Reservation: p.reservation}), nil
}

func (p *assignmentProbe) RevertAdjustment(
	_ context.Context,
	req *connect.Request[pb.RevertAdjustmentRequest],
) (*connect.Response[pb.RevertAdjustmentResponse], error) {
	p.bearers = append(p.bearers, req.Header().Get("Authorization"))
	p.revert = req.Msg
	return connect.NewResponse(&pb.RevertAdjustmentResponse{Reservation: p.reservation}), nil
}

func (p *assignmentProbe) GetReservation(
	_ context.Context,
	req *connect.Request[pb.GetReservationRequest],
) (*connect.Response[pb.GetReservationResponse], error) {
	p.bearers = append(p.bearers, req.Header().Get("Authorization"))
	return connect.NewResponse(&pb.GetReservationResponse{Reservation: p.reservation}), nil
}

func assignmentClient(t *testing.T, p *assignmentProbe) *Credit {
	t.Helper()
	path, handler := creditv1connect.NewCreditReservationServiceHandler(p)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &Credit{Client: creditv1connect.NewCreditReservationServiceClient(server.Client(), server.URL)}
}

// Source: credit/v1/reservation.proto requires immutable adjustment/revert keys,
// private transport authorization and fresh current-state reads after uncertain responses.
func TestAssignmentCreditUsesContractAndCurrentRead(t *testing.T) {
	orderID, offerID, courierID, requesterID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	p := &assignmentProbe{reservation: &pb.Reservation{
		RequestId:      orderID,
		RequesterId:    requesterID,
		OriginalAmount: 20,
		ReservedAmount: 50,
		Status:         pb.ReservationStatus_RESERVATION_STATUS_ASSIGNED,
		ActiveAdjustment: &pb.Adjustment{
			OfferId:        offerID,
			CourierId:      courierID,
			OfferAmount:    50,
			PreviousAmount: 20,
			Status:         pb.AdjustmentStatus_ADJUSTMENT_STATUS_APPLIED,
		},
	}}
	client := assignmentClient(t, p)
	ctx := auth.WithUserBearer(context.Background(), "Bearer private-user-token")
	if err := client.Adjust(
		ctx,
		app.AdjustmentIntent{OrderID: orderID, OfferID: offerID, CourierID: courierID, Amount: 50},
	); err != nil {
		t.Fatal(err)
	}
	if p.adjustment.RequestId != orderID || p.adjustment.OfferId != offerID || p.adjustment.CourierId != courierID ||
		p.adjustment.OfferAmount != 50 {
		t.Fatalf("wrong adjustment: %v", p.adjustment)
	}
	view, err := client.AssignmentReservation(ctx, orderID)
	if err != nil || view.State != "assigned" || view.OfferID != offerID || view.CourierID != courierID ||
		view.ReservedAmount != 50 ||
		view.OriginalAmount != 20 {
		t.Fatalf("assigned view: %+v %v", view, err)
	}
	// Script the financial service's compensating result. This fixture is not a
	// Credit implementation and does not prove ledger/settlement behavior.
	p.reservation.ActiveAdjustment = nil
	p.reservation.ReservedAmount = 20
	p.reservation.Status = pb.ReservationStatus_RESERVATION_STATUS_HELD
	if err := client.Revert(ctx, orderID, offerID); err != nil {
		t.Fatal(err)
	}
	if p.revert.RequestId != orderID || p.revert.OfferId != offerID {
		t.Fatalf("wrong revert: %v", p.revert)
	}
	view, err = client.AssignmentReservation(ctx, orderID)
	if err != nil || view.State != "held" || view.ReservedAmount != 20 || view.OfferID != "" {
		t.Fatalf("held view: %+v %v", view, err)
	}
	for _, bearer := range p.bearers {
		if bearer != "" {
			t.Fatalf("Credit request carried Authorization: %q", bearer)
		}
	}
}

// Source: only credit.v1 INSUFFICIENT_CREDITS is a definitive financial refusal.
func TestAssignmentCreditRefusalDoesNotTrustErrorText(t *testing.T) {
	for _, typed := range []bool{false, true} {
		refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New("INSUFFICIENT_CREDITS"))
		if typed {
			detail, err := connect.NewErrorDetail(
				&errdetails.ErrorInfo{Domain: "credit.v1", Reason: "INSUFFICIENT_CREDITS"},
			)
			if err != nil {
				t.Fatal(err)
			}
			refusal.AddDetail(detail)
		}
		client := assignmentClient(t, &assignmentProbe{refusal: refusal})
		err := client.Adjust(
			context.Background(),
			app.AdjustmentIntent{
				OrderID:   uuid.NewString(),
				OfferID:   uuid.NewString(),
				CourierID: uuid.NewString(),
				Amount:    50,
			},
		)
		if errors.Is(err, app.ErrCreditInsufficient) != typed || (!typed && !errors.Is(err, app.ErrUnavailable)) {
			t.Fatalf("typed=%v err=%v", typed, err)
		}
	}
}

// Source: Credit's ReservationStatus and Adjustment contracts. Inconsistent
// money/identity observations must never authorize local assignment.
func TestAssignmentCreditRejectsInvalidCurrentState(t *testing.T) {
	orderID := uuid.NewString()
	for _, row := range []*pb.Reservation{
		nil,
		{RequestId: uuid.NewString(), Status: pb.ReservationStatus_RESERVATION_STATUS_RELEASED},
		{RequestId: orderID, Status: pb.ReservationStatus_RESERVATION_STATUS_ASSIGNED},
		{RequestId: orderID, RequesterId: uuid.NewString(), OriginalAmount: 20, ReservedAmount: 50, Status: pb.ReservationStatus_RESERVATION_STATUS_HELD},
		{RequestId: orderID, ReservedAmount: 20, Status: pb.ReservationStatus_RESERVATION_STATUS_RELEASED},
	} {
		client := assignmentClient(t, &assignmentProbe{reservation: row})
		if _, err := client.AssignmentReservation(context.Background(), orderID); !errors.Is(err, app.ErrUnavailable) {
			t.Fatalf("accepted inconsistent reservation: %v %v", row, err)
		}
	}
}
