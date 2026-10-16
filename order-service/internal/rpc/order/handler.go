package order

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
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

type Server struct {
	orderv1connect.UnimplementedOrderServiceHandler
	operations Operations
	creator    CreationOperations
}

func NewServer(operations Operations, creator CreationOperations) *Server {
	return &Server{operations: operations, creator: creator}
}

func (s *Server) CreateOrder(ctx context.Context, req *connect.Request[pb.CreateOrderRequest]) (*connect.Response[pb.CreateOrderResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.creator == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	m := req.Msg
	if m.ExpiresAt == nil || m.ExpiresAt.CheckValid() != nil || m.DeliveryDuration == nil || m.DeliveryDuration.CheckValid() != nil || m.Delivery == nil {
		return nil, shared.ConnectError(ctx, app.ErrInvalid)
	}
	ctx = clients.WithUserBearer(ctx, req.Header().Get("Authorization"))
	result, err := s.creator.Create(ctx, caller, app.CreateInput{
		Key: m.IdempotencyKey, PickupID: m.PickupLocationId, DeliveryID: m.Delivery.LocationId,
		Instructions: m.Delivery.Instructions, ItemDescription: m.ItemDescription, ImageIDs: m.ItemImageIds,
		RewardCredits: m.RewardCredits, DeliveryDuration: m.DeliveryDuration.AsDuration(), ExpiresAt: m.ExpiresAt.AsTime(),
	})
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.CreateOrderResponse{Order: shared.Order(result)}), nil
}

func (s *Server) GetOrder(ctx context.Context, req *connect.Request[pb.GetOrderRequest]) (*connect.Response[pb.GetOrderResponse], error) {
	o, err := s.operations.GetOrder(ctx, shared.Caller(ctx), req.Msg.GetId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	return connect.NewResponse(&pb.GetOrderResponse{Order: shared.Order(o)}), nil
}

func (s *Server) ListOpenOrders(ctx context.Context, req *connect.Request[pb.ListOpenOrdersRequest]) (*connect.Response[pb.ListOpenOrdersResponse], error) {
	m := req.Msg
	r := app.ListRequest{Page: app.Page{Number: m.GetPage(), Size: m.GetPageSize()}, PickupID: m.GetPickupLocationId(), DeliveryID: m.GetDeliveryLocationId()}

	p, err := s.operations.ListOpenOrders(ctx, shared.Caller(ctx), r)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	return connect.NewResponse(&pb.ListOpenOrdersResponse{Orders: shared.Orders(p.Orders), PageInfo: shared.PageInfo(p.PageInfo)}), nil
}

func (s *Server) ListMyOrders(ctx context.Context, req *connect.Request[pb.ListMyOrdersRequest]) (*connect.Response[pb.ListMyOrdersResponse], error) {
	m := req.Msg
	r, err := shared.ListRequest(m.GetPage(), m.GetPageSize(), m.GetStatus(), m.GetRole())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	p, err := s.operations.ListMyOrders(ctx, shared.Caller(ctx), r)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	return connect.NewResponse(&pb.ListMyOrdersResponse{Orders: shared.Orders(p.Orders), PageInfo: shared.PageInfo(p.PageInfo)}), nil
}

func (s *Server) ListOffers(ctx context.Context, req *connect.Request[pb.ListOffersRequest]) (*connect.Response[pb.ListOffersResponse], error) {
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
