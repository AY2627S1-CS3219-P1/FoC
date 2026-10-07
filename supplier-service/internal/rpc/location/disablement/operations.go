package disablement

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

func (h *Server) CreateDisablement(ctx context.Context, r *connect.Request[pb.CreateDisablementRequest]) (*connect.Response[pb.CreateDisablementResponse], error) {
	d, e := h.operations.CreateDisablement(ctx, h.principal(ctx), app.CreateDisablement{LocationID: r.Msg.LocationId, StartsAt: rpcshared.Absolute(r.Msg.StartsAt), EndsAt: rpcshared.Absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Key: r.Msg.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CreateDisablementResponse{Disablement: rpcshared.Disablement(d, h.clock())}), nil
}

func (h *Server) ListDisablements(ctx context.Context, r *connect.Request[pb.ListDisablementsRequest]) (*connect.Response[pb.ListDisablementsResponse], error) {
	p, e := h.operations.ListDisablements(ctx, h.principal(ctx), r.Msg.LocationId, stateFromWire[r.Msg.State], app.Page{Number: r.Msg.Page, Size: r.Msg.PageSize})
	if e != nil {
		return nil, e
	}
	out := &pb.ListDisablementsResponse{PageInfo: rpcshared.PageInfo(p.PageInfo)}
	for _, d := range p.Items {
		out.Disablements = append(out.Disablements, rpcshared.Disablement(d, h.clock()))
	}
	return connect.NewResponse(out), nil
}

func (h *Server) UpdateDisablement(ctx context.Context, r *connect.Request[pb.UpdateDisablementRequest]) (*connect.Response[pb.UpdateDisablementResponse], error) {
	d, e := h.operations.UpdateDisablement(ctx, h.principal(ctx), app.UpdateDisablement{ID: r.Msg.Id, StartsAt: rpcshared.Absolute(r.Msg.StartsAt), EndsAt: rpcshared.Absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Paths: r.Msg.GetUpdateMask().GetPaths(), ExpectedRevision: r.Msg.ExpectedRevision})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.UpdateDisablementResponse{Disablement: rpcshared.Disablement(d, h.clock())}), nil
}

func (h *Server) EndDisablement(ctx context.Context, r *connect.Request[pb.EndDisablementRequest]) (*connect.Response[pb.EndDisablementResponse], error) {
	d, e := h.operations.EndDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.EndDisablementResponse{Disablement: rpcshared.Disablement(d, h.clock())}), nil
}

func (h *Server) CancelDisablement(ctx context.Context, r *connect.Request[pb.CancelDisablementRequest]) (*connect.Response[pb.CancelDisablementResponse], error) {
	d, e := h.operations.CancelDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CancelDisablementResponse{Disablement: rpcshared.Disablement(d, h.clock())}), nil
}
