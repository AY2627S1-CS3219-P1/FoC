package shared

import (
	"context"
	"github.com/google/uuid"
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

// Enum mappings keep protobuf values at the RPC boundary.
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

// Request conversion rejects enum values outside the domain contract.
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

// Response conversion exposes domain orders and offers as protobuf views.
func Order(o app.Order) *pb.Order {
	var status pb.OrderStatus
	for value, domain := range statuses {
		if domain == o.Status {
			status = value
			break
		}
	}

	return &pb.Order{
		Id:          o.ID,
		RequesterId: o.RequesterID,
		CourierId:   o.CourierID,
		Status:      status,
		Pickup:      location(o.Pickup),
		Delivery: &pb.DeliveryDetails{
			Location:     location(o.Delivery),
			Instructions: o.DeliveryInstructions,
		},
		ItemDescription:      o.ItemDescription,
		RewardCredits:        o.RewardCredits,
		DeliveryDuration:     durationpb.New(o.DeliveryDuration),
		ExpiresAt:            timestamppb.New(o.ExpiresAt),
		AssignedAt:           timestamp(o.AssignedAt),
		PickedUpAt:           timestamp(o.PickedUpAt),
		DeliveredAt:          timestamp(o.DeliveredAt),
		CompletedAt:          timestamp(o.CompletedAt),
		ClosedAt:             timestamp(o.ClosedAt),
		CloseReason:          o.CloseReason,
		UnattendedDelivery:   o.UnattendedDelivery,
		ItemLocation:         o.ItemLocation,
		Revision:             o.Revision,
		CreatedAt:            timestamppb.New(o.CreatedAt),
		UpdatedAt:            timestamppb.New(o.UpdatedAt),
		DeliveryBuildingName: o.Delivery.BuildingName,
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

	return &pb.Offer{
		Id:        o.ID,
		OrderId:   o.OrderID,
		CourierId: o.CourierID,
		Credits:   o.Credits,
		Status:    status,
		CreatedAt: timestamppb.New(o.CreatedAt),
		UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
}

func PageInfo(p app.PageInfo) *pb.PageInfo {
	return &pb.PageInfo{Page: p.Number, PageSize: p.Size, TotalItems: p.Total, TotalPages: p.TotalPages()}
}

// Value helpers preserve optional location fields and timestamps.
func location(l app.LocationSnapshot) *pb.LocationSnapshot {
	return &pb.LocationSnapshot{LocationId: l.LocationID, Name: l.Name, BuildingName: l.BuildingName, Floor: l.Floor}
}

func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}

	return timestamppb.New(*t)
}

// ItemImageViews is owned by the RPC projection boundary.
type ItemImageViews interface {
	ForOrder(context.Context, app.Caller, app.Order) ([]app.ItemImageView, error)
}

func OrderView(ctx context.Context, caller app.Caller, o app.Order, images ItemImageViews) (*pb.Order, error) {
	callerID := caller.ID
	if id, err := uuid.Parse(callerID); err == nil {
		callerID = id.String()
	}
	caller.ID = callerID
	result := Order(app.ProjectForCaller(caller, o))
	if !o.HasParticipant(caller.ID) {
		result.Delivery = nil
	}
	// Historical/admin-only views retain the Order even when later attachments
	// are not authorized. Never mint URLs merely because the caller is an admin.
	if images == nil || (!o.HasParticipant(caller.ID) && (caller.Suspended || o.Status != app.AwaitingCourier || !o.ExpiresAt.After(time.Now()))) {
		return result, nil
	}
	views, err := images.ForOrder(ctx, caller, o)
	if err != nil {
		return nil, err
	}
	for _, image := range views {
		result.ItemImages = append(result.ItemImages, &pb.ItemImage{Id: image.ID, DownloadUrl: image.DownloadURL})
	}
	return result, nil
}

func OrderViews(ctx context.Context, caller app.Caller, orders []app.Order, images ItemImageViews) ([]*pb.Order, error) {
	result := make([]*pb.Order, 0, len(orders))
	for _, o := range orders {
		view, err := OrderView(ctx, caller, o, images)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}
