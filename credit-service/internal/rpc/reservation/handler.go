// Package reservation serves credit.v1.CreditReservationService to Order.
package reservation

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

// Reservations lists the reservation operations the handler calls.
type Reservations interface {
	Reserve(ctx context.Context, orderID, requesterID string, amount int64) (reservation.Reservation, error)
	AdjustReservation(
		ctx context.Context,
		orderID, assignmentID, courierID string,
		offerAmount int64,
	) (reservation.Reservation, error)
	RevertAdjustment(ctx context.Context, orderID, assignmentID string) (reservation.Reservation, error)
	ReleaseReservation(
		ctx context.Context,
		orderID string,
		reason reservation.ReleaseReason,
	) (reservation.Reservation, error)
	GetReservation(ctx context.Context, orderID string) (reservation.Reservation, error)
}

type Server struct {
	creditv1connect.UnimplementedCreditReservationServiceHandler
	Reservations Reservations
}

func (s *Server) Reserve(
	ctx context.Context,
	req *connect.Request[creditv1.ReserveRequest],
) (*connect.Response[creditv1.ReserveResponse], error) {
	r, err := s.Reservations.Reserve(ctx, req.Msg.GetRequestId(), req.Msg.GetRequesterId(), req.Msg.GetAmount())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.ReserveResponse{Reservation: shared.Reservation(r)}), nil
}

func (s *Server) AdjustReservation(
	ctx context.Context,
	req *connect.Request[creditv1.AdjustReservationRequest],
) (*connect.Response[creditv1.AdjustReservationResponse], error) {
	m := req.Msg
	r, err := s.Reservations.AdjustReservation(
		ctx,
		m.GetRequestId(),
		m.GetOfferId(),
		m.GetCourierId(),
		m.GetOfferAmount(),
	)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.AdjustReservationResponse{Reservation: shared.Reservation(r)}), nil
}

func (s *Server) RevertAdjustment(
	ctx context.Context,
	req *connect.Request[creditv1.RevertAdjustmentRequest],
) (*connect.Response[creditv1.RevertAdjustmentResponse], error) {
	r, err := s.Reservations.RevertAdjustment(ctx, req.Msg.GetRequestId(), req.Msg.GetOfferId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.RevertAdjustmentResponse{Reservation: shared.Reservation(r)}), nil
}

func (s *Server) ReleaseReservation(
	ctx context.Context,
	req *connect.Request[creditv1.ReleaseReservationRequest],
) (*connect.Response[creditv1.ReleaseReservationResponse], error) {
	reason := shared.ReleaseReason(req.Msg.GetReason())
	r, err := s.Reservations.ReleaseReservation(ctx, req.Msg.GetRequestId(), reason)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.ReleaseReservationResponse{Reservation: shared.Reservation(r)}), nil
}

func (s *Server) GetReservation(
	ctx context.Context,
	req *connect.Request[creditv1.GetReservationRequest],
) (*connect.Response[creditv1.GetReservationResponse], error) {
	r, err := s.Reservations.GetReservation(ctx, req.Msg.GetRequestId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.GetReservationResponse{Reservation: shared.Reservation(r)}), nil
}
