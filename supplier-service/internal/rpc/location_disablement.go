package rpc

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
)

func (h *WorkflowServer) CreateDisablement(ctx context.Context, r *connect.Request[pb.CreateDisablementRequest]) (*connect.Response[pb.CreateDisablementResponse], error) {
	d, e := h.operations.CreateDisablement(ctx, h.principal(ctx), app.CreateDisablement{LocationID: r.Msg.LocationId, StartsAt: absolute(r.Msg.StartsAt), EndsAt: absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Key: r.Msg.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CreateDisablementResponse{Disablement: h.toProtoDisablement(d)}), nil
}

func (h *WorkflowServer) ListDisablements(ctx context.Context, r *connect.Request[pb.ListDisablementsRequest]) (*connect.Response[pb.ListDisablementsResponse], error) {
	p, e := h.operations.ListDisablements(ctx, h.principal(ctx), r.Msg.LocationId, stateFromWire[r.Msg.State], app.Page{Number: r.Msg.Page, Size: r.Msg.PageSize})
	if e != nil {
		return nil, e
	}
	out := &pb.ListDisablementsResponse{PageInfo: toProtoPageInfo(p.PageInfo)}
	for _, d := range p.Items {
		out.Disablements = append(out.Disablements, h.toProtoDisablement(d))
	}
	return connect.NewResponse(out), nil
}

func (h *WorkflowServer) UpdateDisablement(ctx context.Context, r *connect.Request[pb.UpdateDisablementRequest]) (*connect.Response[pb.UpdateDisablementResponse], error) {
	d, e := h.operations.UpdateDisablement(ctx, h.principal(ctx), app.UpdateDisablement{ID: r.Msg.Id, StartsAt: absolute(r.Msg.StartsAt), EndsAt: absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Paths: r.Msg.GetUpdateMask().GetPaths(), ExpectedRevision: r.Msg.ExpectedRevision})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.UpdateDisablementResponse{Disablement: h.toProtoDisablement(d)}), nil
}

func (h *WorkflowServer) EndDisablement(ctx context.Context, r *connect.Request[pb.EndDisablementRequest]) (*connect.Response[pb.EndDisablementResponse], error) {
	d, e := h.operations.EndDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.EndDisablementResponse{Disablement: h.toProtoDisablement(d)}), nil
}

func (h *WorkflowServer) CancelDisablement(ctx context.Context, r *connect.Request[pb.CancelDisablementRequest]) (*connect.Response[pb.CancelDisablementResponse], error) {
	d, e := h.operations.CancelDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CancelDisablementResponse{Disablement: h.toProtoDisablement(d)}), nil
}
