package order

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Operations contains only the reads consumed by this RPC service.
type Operations interface {
	GetOrder(context.Context, app.Caller, string) (app.Order, error)
	ListOpenOrders(context.Context, app.Caller, app.ListRequest) (app.OrderPage, error)
	ListMyOrders(context.Context, app.Caller, app.ListRequest) (app.OrderPage, error)
	ListOffers(context.Context, app.Caller, string, app.OfferStatus) ([]app.Offer, error)
}

type CreationOperations interface {
	Create(context.Context, app.Caller, app.CreateInput) (app.Order, error)
}

type ImageOperations interface {
	shared.ItemImageViews
	CreateUpload(context.Context, app.Caller, string, int64) (app.ItemImageUpload, images.UploadGrant, error)
}

type Server struct {
	orderv1connect.UnimplementedOrderServiceHandler
	operations    Operations
	creator       CreationOperations
	images        ImageOperations
	assignments   AssignmentOperations
	cancellations CancellationOperations
	progress      ProgressOperations
	disposition   DispositionOperations
	proofs        ProofOperations
	proofReceipts ProofReceiptOperations
}

func NewServer(
	operations Operations,
	creator CreationOperations,
	images ImageOperations,
	assignments ...AssignmentOperations,
) *Server {
	s := &Server{operations: operations, creator: creator, images: images}
	if len(assignments) > 0 {
		s.assignments = assignments[0]
	}
	return s
}

func (s *Server) CreateOrder(
	ctx context.Context,
	req *connect.Request[pb.CreateOrderRequest],
) (*connect.Response[pb.CreateOrderResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.creator == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	m := req.Msg
	if m.ExpiresAt == nil || m.ExpiresAt.CheckValid() != nil || m.DeliveryDuration == nil ||
		m.DeliveryDuration.CheckValid() != nil ||
		m.Delivery == nil {
		return nil, shared.ConnectError(ctx, app.ErrInvalid)
	}
	result, err := s.creator.Create(ctx, caller, app.CreateInput{
		Key:              m.IdempotencyKey,
		PickupID:         m.PickupLocationId,
		DeliveryID:       m.Delivery.LocationId,
		Instructions:     m.Delivery.Instructions,
		ItemDescription:  m.ItemDescription,
		ImageIDs:         m.ItemImageIds,
		RewardCredits:    m.RewardCredits,
		DeliveryDuration: m.DeliveryDuration.AsDuration(),
		ExpiresAt:        m.ExpiresAt.AsTime(),
	})
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, result, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.CreateOrderResponse{Order: view}), nil
}

func (s *Server) GetOrder(
	ctx context.Context,
	req *connect.Request[pb.GetOrderRequest],
) (*connect.Response[pb.GetOrderResponse], error) {
	o, err := s.operations.GetOrder(ctx, shared.Caller(ctx), req.Msg.GetId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	view, err := shared.OrderView(ctx, shared.Caller(ctx), o, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.GetOrderResponse{Order: view}), nil
}

func (s *Server) ListOpenOrders(
	ctx context.Context,
	req *connect.Request[pb.ListOpenOrdersRequest],
) (*connect.Response[pb.ListOpenOrdersResponse], error) {
	m := req.Msg
	//nolint:staticcheck // Reject presence of the legacy exact filter instead of silently ignoring it.
	if m.DeliveryLocationId != nil {
		return nil, shared.ConnectError(ctx, app.ErrInvalid)
	}
	r := app.ListRequest{
		Page:                 app.Page{Number: m.GetPage(), Size: m.GetPageSize()},
		PickupID:             m.GetPickupLocationId(),
		DeliveryBuildingName: m.GetDeliveryBuildingName(),
	}

	p, err := s.operations.ListOpenOrders(ctx, shared.Caller(ctx), r)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	views, err := shared.OrderViews(ctx, shared.Caller(ctx), p.Orders, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ListOpenOrdersResponse{Orders: views, PageInfo: shared.PageInfo(p.PageInfo)}), nil
}

func (s *Server) ListMyOrders(
	ctx context.Context,
	req *connect.Request[pb.ListMyOrdersRequest],
) (*connect.Response[pb.ListMyOrdersResponse], error) {
	m := req.Msg
	r, err := shared.ListRequest(m.GetPage(), m.GetPageSize(), m.GetStatus(), m.GetRole())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	p, err := s.operations.ListMyOrders(ctx, shared.Caller(ctx), r)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	views, err := shared.OrderViews(ctx, shared.Caller(ctx), p.Orders, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ListMyOrdersResponse{Orders: views, PageInfo: shared.PageInfo(p.PageInfo)}), nil
}

func (s *Server) ListOffers(
	ctx context.Context,
	req *connect.Request[pb.ListOffersRequest],
) (*connect.Response[pb.ListOffersResponse], error) {
	status, err := shared.OfferStatus(req.Msg.GetStatus())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	offers, err := s.operations.ListOffers(ctx, shared.Caller(ctx), req.Msg.GetOrderId(), status)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	out := make([]*pb.Offer, len(offers))
	for i, offer := range offers {
		out[i] = shared.Offer(offer)
	}

	return connect.NewResponse(&pb.ListOffersResponse{Offers: out}), nil
}

func (s *Server) CreateItemImageUpload(
	ctx context.Context,
	req *connect.Request[pb.CreateItemImageUploadRequest],
) (*connect.Response[pb.CreateItemImageUploadResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.images == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	image, grant, err := s.images.CreateUpload(ctx, caller, req.Msg.ContentType, req.Msg.SizeBytes)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(
		&pb.CreateItemImageUploadResponse{
			ImageId:       image.ID,
			UploadUrl:     grant.URL,
			ExpiresAt:     timestamppb.New(grant.ExpiresAt),
			UploadHeaders: grant.Headers,
		},
	), nil
}
