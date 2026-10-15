package shared

import (
	"context"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Caller(ctx context.Context) app.Caller {
	c, _ := auth.CallerFromContext(ctx)
	return app.Caller{ID: c.ID, Admin: c.Admin, Suspended: c.Role == "suspended_user"}
}

var statuses = map[pb.OrderStatus]app.Status{
	pb.OrderStatus_ORDER_STATUS_UNSPECIFIED:         "",
	pb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION: app.PendingReservation,
	pb.OrderStatus_ORDER_STATUS_AWAITING_COURIER:    app.AwaitingCourier,
	pb.OrderStatus_ORDER_STATUS_AWAITING_PICKUP:     app.AwaitingPickup,
	pb.OrderStatus_ORDER_STATUS_DELIVERING:          app.Delivering,
	pb.OrderStatus_ORDER_STATUS_DELIVERED:           app.Delivered,
	pb.OrderStatus_ORDER_STATUS_COMPLETED:           app.Completed,
	pb.OrderStatus_ORDER_STATUS_CANCELLED:           app.Cancelled,
	pb.OrderStatus_ORDER_STATUS_EXPIRED:             app.Expired,
	pb.OrderStatus_ORDER_STATUS_FAILED:              app.Failed,
	pb.OrderStatus_ORDER_STATUS_REJECTED:            app.Rejected,
}

var roles = map[pb.ParticipantRole]app.Role{
	pb.ParticipantRole_PARTICIPANT_ROLE_UNSPECIFIED: app.AnyRole,
	pb.ParticipantRole_PARTICIPANT_ROLE_REQUESTER:   app.RequesterRole,
	pb.ParticipantRole_PARTICIPANT_ROLE_COURIER:     app.CourierRole,
}

var offerStatuses = map[pb.OfferStatus]app.OfferStatus{
	pb.OfferStatus_OFFER_STATUS_UNSPECIFIED: "",
	pb.OfferStatus_OFFER_STATUS_OPEN:        app.OfferOpen,
	pb.OfferStatus_OFFER_STATUS_ACCEPTING:   app.OfferAccepting,
	pb.OfferStatus_OFFER_STATUS_ACCEPTED:    app.OfferAccepted,
	pb.OfferStatus_OFFER_STATUS_VOID:        app.OfferVoid,
}

func ListRequest(page, size int32, status pb.OrderStatus, role pb.ParticipantRole) (app.ListRequest, error) {
	s, validStatus := statuses[status]
	r, validRole := roles[role]
	if !validStatus || !validRole {
		return app.ListRequest{}, app.ErrInvalid
	}
	return app.ListRequest{Page: app.Page{Number: page, Size: size}, Status: s, Role: r}, nil
}

func OfferStatus(status pb.OfferStatus) (app.OfferStatus, error) {
	s, ok := offerStatuses[status]
	if !ok {
		return "", app.ErrInvalid
	}
	return s, nil
}

func Order(o app.Order) *pb.Order {
	var status pb.OrderStatus
	for value, domain := range statuses {
		if domain == o.Status {
			status = value
			break
		}
	}
	return &pb.Order{
		Id: o.ID, RequesterId: o.RequesterID, CourierId: o.CourierID, Status: status,
		Pickup: location(o.Pickup), Delivery: &pb.DeliveryDetails{Location: location(o.Delivery), Instructions: o.DeliveryInstructions},
		ItemDescription: o.ItemDescription, RewardCredits: o.RewardCredits,
		DeliveryDuration: durationpb.New(o.DeliveryDuration), ExpiresAt: timestamppb.New(o.ExpiresAt),
		AssignedAt: timestamp(o.AssignedAt), PickedUpAt: timestamp(o.PickedUpAt), DeliveredAt: timestamp(o.DeliveredAt),
		CompletedAt: timestamp(o.CompletedAt), ClosedAt: timestamp(o.ClosedAt), CloseReason: o.CloseReason,
		UnattendedDelivery: o.UnattendedDelivery, ItemLocation: o.ItemLocation, Revision: o.Revision,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}

func Orders(orders []app.Order) []*pb.Order {
	out := make([]*pb.Order, len(orders))
	for i, o := range orders {
		out[i] = Order(o)
	}
	return out
}

func Offer(o app.Offer) *pb.Offer {
	var status pb.OfferStatus
	for value, domain := range offerStatuses {
		if domain == o.Status {
			status = value
			break
		}
	}
	return &pb.Offer{Id: o.ID, OrderId: o.OrderID, CourierId: o.CourierID, Credits: o.Credits, Status: status,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt)}
}

func PageInfo(p app.PageInfo) *pb.PageInfo {
	return &pb.PageInfo{Page: p.Number, PageSize: p.Size, TotalItems: p.Total, TotalPages: p.TotalPages()}
}

func location(l app.LocationSnapshot) *pb.LocationSnapshot {
	return &pb.LocationSnapshot{LocationId: l.LocationID, Name: l.Name, BuildingName: l.BuildingName, Floor: l.Floor}
}

func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
