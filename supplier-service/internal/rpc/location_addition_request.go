package rpc

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"google.golang.org/genproto/googleapis/type/timeofday"
)

func (h *WorkflowServer) SubmitLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.SubmitLocationAdditionRequestRequest]) (*connect.Response[pb.SubmitLocationAdditionRequestResponse], error) {
	v, e := h.operations.SubmitRequest(ctx, h.principal(ctx), app.SubmitRequest{Proposal: proposal(r.Msg.Proposal), Key: r.Msg.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.SubmitLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v)}), nil
}

func (h *WorkflowServer) GetLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.GetLocationAdditionRequestRequest]) (*connect.Response[pb.GetLocationAdditionRequestResponse], error) {
	v, e := h.operations.GetRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.GetLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v)}), nil
}

func (h *WorkflowServer) ListLocationAdditionRequests(ctx context.Context, r *connect.Request[pb.ListLocationAdditionRequestsRequest]) (*connect.Response[pb.ListLocationAdditionRequestsResponse], error) {
	p, e := h.operations.ListRequests(ctx, h.principal(ctx), statusFromWire[r.Msg.Status], app.Page{Number: r.Msg.Page, Size: r.Msg.PageSize})
	if e != nil {
		return nil, e
	}
	out := &pb.ListLocationAdditionRequestsResponse{PageInfo: toProtoPageInfo(p.PageInfo)}
	for _, v := range p.Items {
		out.Requests = append(out.Requests, toProtoAdditionRequest(v))
	}
	return connect.NewResponse(out), nil
}

func (h *WorkflowServer) UpdateLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.UpdateLocationAdditionRequestRequest]) (*connect.Response[pb.UpdateLocationAdditionRequestResponse], error) {
	// Presence belongs to transport translation: absent coordinates/classification
	// must not silently become a valid zero coordinate or false classification.
	if r.Msg.Proposal == nil {
		return nil, app.ErrInvalidArgument
	}
	for _, p := range r.Msg.GetUpdateMask().GetPaths() {
		// Patch messages skip full-input validation. Validate selected times before
		// conversion so seconds or invalid nanos cannot be truncated into valid input.
		var value *timeofday.TimeOfDay
		switch p {
		case "opens_at":
			value = r.Msg.Proposal.OpensAt
		case "closes_at":
			value = r.Msg.Proposal.ClosesAt
		}
		if value != nil && (value.Hours < 0 || value.Hours >= 24 || value.Minutes < 0 || value.Minutes >= 60 || value.Seconds != 0 || value.Nanos != 0) {
			return nil, app.ErrInvalidArgument
		}
		if p == "coordinates" && r.Msg.GetProposal().GetCoordinates() == nil {
			return nil, app.ErrInvalidArgument
		}
		if p == "is_supplier" && (r.Msg.Proposal == nil || r.Msg.Proposal.IsSupplier == nil) {
			return nil, app.ErrInvalidArgument
		}
	}
	v, e := h.operations.UpdateRequest(ctx, h.principal(ctx), app.UpdateRequest{ID: r.Msg.Id, Proposal: proposal(r.Msg.Proposal), Paths: proposalPaths(r.Msg.GetUpdateMask().GetPaths()), ExpectedRevision: r.Msg.ExpectedRevision})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.UpdateLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v)}), nil
}

func (h *WorkflowServer) WithdrawLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.WithdrawLocationAdditionRequestRequest]) (*connect.Response[pb.WithdrawLocationAdditionRequestResponse], error) {
	v, e := h.operations.WithdrawRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.WithdrawLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v)}), nil
}

func (h *WorkflowServer) ApproveLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.ApproveLocationAdditionRequestRequest]) (*connect.Response[pb.ApproveLocationAdditionRequestResponse], error) {
	v, e := h.operations.ApproveRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ApproveLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v.Request), Location: h.toProtoWorkflowLocation(v.Location)}), nil
}

func (h *WorkflowServer) RejectLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.RejectLocationAdditionRequestRequest]) (*connect.Response[pb.RejectLocationAdditionRequestResponse], error) {
	v, e := h.operations.RejectRequest(ctx, h.principal(ctx), r.Msg.Id, r.Msg.ReviewNote)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.RejectLocationAdditionRequestResponse{Request: toProtoAdditionRequest(v)}), nil
}
