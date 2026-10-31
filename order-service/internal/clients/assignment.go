package clients

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
)

func (c *Credit) Adjust(ctx context.Context, intent app.AdjustmentIntent) error {
	request := connect.NewRequest(&creditpb.AdjustReservationRequest{
		RequestId:   intent.OrderID,
		OfferId:     intent.OfferID,
		CourierId:   intent.CourierID,
		OfferAmount: intent.Amount,
	})
	response, err := c.Client.AdjustReservation(ctx, request)
	if err != nil {
		if hasCreditReason(err, connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS") {
			return app.ErrCreditInsufficient
		}
		return app.ErrUnavailable
	}
	if response == nil || response.Msg == nil || response.Msg.Reservation == nil ||
		response.Msg.Reservation.RequestId != intent.OrderID {
		return app.ErrUnavailable
	}
	return nil
}

func (c *Credit) Revert(ctx context.Context, orderID, attemptID string) error {
	request := connect.NewRequest(&creditpb.RevertAdjustmentRequest{RequestId: orderID, OfferId: attemptID})
	response, err := c.Client.RevertAdjustment(ctx, request)
	if err != nil || response == nil || response.Msg == nil || response.Msg.Reservation == nil ||
		response.Msg.Reservation.RequestId != orderID {
		return app.ErrUnavailable
	}
	return nil
}

// AssignmentReservation uses a fresh read: an idempotent mutation response may
// describe an adjustment that has since been compensated or settled.
func (c *Credit) AssignmentReservation(ctx context.Context, orderID string) (app.AssignmentReservation, error) {
	request := connect.NewRequest(&creditpb.GetReservationRequest{RequestId: orderID})
	response, err := c.Client.GetReservation(ctx, request)
	if err != nil || response == nil || response.Msg == nil || response.Msg.Reservation == nil {
		return app.AssignmentReservation{}, app.ErrUnavailable
	}
	v := response.Msg.Reservation
	if v.RequestId != orderID || v.OriginalAmount < 0 || v.ReservedAmount < 0 {
		return app.AssignmentReservation{}, app.ErrUnavailable
	}
	view := app.AssignmentReservation{
		OrderID:        v.RequestId,
		RequesterID:    v.RequesterId,
		OriginalAmount: v.OriginalAmount,
		ReservedAmount: v.ReservedAmount,
	}
	switch v.Status {
	case creditpb.ReservationStatus_RESERVATION_STATUS_HELD:
		if v.ActiveAdjustment != nil || v.Settlement != nil || v.OriginalAmount <= 0 || v.RequesterId == "" ||
			v.ReservedAmount != v.OriginalAmount {
			return app.AssignmentReservation{}, app.ErrUnavailable
		}
		view.State = "held"
	case creditpb.ReservationStatus_RESERVATION_STATUS_ASSIGNED:
		a := v.ActiveAdjustment
		if a == nil || a.Status != creditpb.AdjustmentStatus_ADJUSTMENT_STATUS_APPLIED || a.OfferId == "" ||
			a.CourierId == "" ||
			a.OfferAmount != v.ReservedAmount ||
			a.PreviousAmount != v.OriginalAmount ||
			v.RequesterId == "" ||
			v.OriginalAmount <= 0 ||
			v.Settlement != nil {
			return app.AssignmentReservation{}, app.ErrUnavailable
		}
		view.State, view.OfferID, view.CourierID = "assigned", a.OfferId, a.CourierId
	case creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED:
		if v.ReservedAmount != 0 || v.ActiveAdjustment != nil {
			return app.AssignmentReservation{}, app.ErrUnavailable
		}
		view.State = "released"
	default:
		return app.AssignmentReservation{}, app.ErrUnavailable
	}
	return view, nil
}

var _ app.AssignmentCredit = (*Credit)(nil)
