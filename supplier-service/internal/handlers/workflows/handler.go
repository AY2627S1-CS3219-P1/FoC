// Package workflows translates Supplier operational Connect contracts. JWT
// verification is supplied by the authenticated service boundary.
package workflows

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Operations is the application boundary consumed by this transport.
type Operations interface {
	CreateDisablement(context.Context, app.Caller, app.CreateDisablement) (app.Disablement, error)
	ListDisablements(context.Context, app.Caller, string, app.DisablementState, app.Page) (app.DisablementPage, error)
	UpdateDisablement(context.Context, app.Caller, app.UpdateDisablement) (app.Disablement, error)
	EndDisablement(context.Context, app.Caller, string) (app.Disablement, error)
	CancelDisablement(context.Context, app.Caller, string) (app.Disablement, error)
	SubmitRequest(context.Context, app.Caller, app.SubmitRequest) (app.AdditionRequest, error)
	ListRequests(context.Context, app.Caller, app.RequestStatus, app.Page) (app.RequestPage, error)
	GetRequest(context.Context, app.Caller, string) (app.AdditionRequest, error)
	UpdateRequest(context.Context, app.Caller, app.UpdateRequest) (app.AdditionRequest, error)
	WithdrawRequest(context.Context, app.Caller, string) (app.AdditionRequest, error)
	ApproveRequest(context.Context, app.Caller, string) (app.Approval, error)
	RejectRequest(context.Context, app.Caller, string, string) (app.AdditionRequest, error)
}

// Principal reads only the already verified caller from the request context.
// It must not trust a caller ID or role supplied by request headers/body.
type Principal func(context.Context) app.Caller

type Handler struct {
	rpc.UnimplementedLocationDiscoveryServiceHandler
	rpc.UnimplementedLocationAdminServiceHandler
	rpc.UnimplementedLocationDisablementServiceHandler
	rpc.UnimplementedLocationAdditionRequestServiceHandler
	operations Operations
	principal  Principal
	clock      func() time.Time
}

func New(operations Operations, principal Principal, clock func() time.Time) *Handler {
	if operations == nil || principal == nil || clock == nil {
		panic("workflow handler dependencies are required")
	}
	return &Handler{operations: operations, principal: principal, clock: clock}
}

// Mount requires an HTTP authentication boundary. The production composition
// root must supply JWT middleware and a Principal accessor before mounting.
// No unauthenticated/default-identity production fallback is provided.
func Mount(mux *http.ServeMux, handler *Handler, authenticate func(http.Handler) http.Handler) error {
	if authenticate == nil {
		return errors.New("workflow JWT authentication boundary is required")
	}
	validator, err := protovalidate.New()
	if err != nil {
		return err
	}
	options := []connect.HandlerOption{connect.WithInterceptors(handler.interceptor(validator))}
	path, h := rpc.NewLocationDiscoveryServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	path, h = rpc.NewLocationAdminServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	path, h = rpc.NewLocationDisablementServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	path, h = rpc.NewLocationAdditionRequestServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	return nil
}
func (h *Handler) interceptor(validator protovalidate.Validator) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c := h.principal(ctx)
			if !c.Authenticated() {
				return nil, connectError(ctx, app.ErrUnauthenticated)
			}
			procedure := req.Spec().Procedure
			adminOnly := false
			switch procedure {
			case rpc.LocationAdminServiceCreateLocationProcedure,
				rpc.LocationAdminServiceUpdateLocationProcedure,
				rpc.LocationAdminServiceArchiveLocationProcedure,
				rpc.LocationAdminServiceUnarchiveLocationProcedure,
				rpc.LocationDisablementServiceCreateDisablementProcedure,
				rpc.LocationDisablementServiceListDisablementsProcedure,
				rpc.LocationDisablementServiceUpdateDisablementProcedure,
				rpc.LocationDisablementServiceEndDisablementProcedure,
				rpc.LocationDisablementServiceCancelDisablementProcedure,
				rpc.LocationAdditionRequestServiceApproveLocationAdditionRequestProcedure,
				rpc.LocationAdditionRequestServiceRejectLocationAdditionRequestProcedure:
				adminOnly = true
			}
			if adminOnly && !c.IsAdmin() {
				return nil, connectError(ctx, app.ErrPermissionDenied)
			}
			if procedure == rpc.LocationAdditionRequestServiceSubmitLocationAdditionRequestProcedure && c.Role == "suspended_user" {
				return nil, connectError(ctx, app.ErrPermissionDenied)
			}
			message, ok := req.Any().(proto.Message)
			if !ok {
				return nil, connectError(ctx, app.ErrInvalidArgument)
			}
			normalizeText(message)
			if err := validator.Validate(message); err != nil {
				return nil, connectError(ctx, app.ErrInvalidArgument)
			}
			response, err := next(ctx, req)
			return response, connectError(ctx, err)
		}
	})
}

// Normalize transport text before declarative length checks. Application
// operations independently enforce the same normalized rules for non-RPC callers.
func normalizeText(message proto.Message) {
	var p *pb.LocationInput
	switch m := message.(type) {
	case *pb.CreateDisablementRequest:
		m.Reason = strings.TrimSpace(m.Reason)
	case *pb.UpdateDisablementRequest:
		m.Reason = strings.TrimSpace(m.Reason)
	case *pb.RejectLocationAdditionRequestRequest:
		m.ReviewNote = strings.TrimSpace(m.ReviewNote)
	case *pb.SubmitLocationAdditionRequestRequest:
		p = m.Proposal
	case *pb.UpdateLocationAdditionRequestRequest:
		p = m.Proposal
	}
	if p == nil {
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Details = strings.TrimSpace(p.Details)
	if p.Floor != nil {
		*p.Floor = strings.TrimSpace(*p.Floor)
	}
	if p.Contact != nil {
		*p.Contact = strings.TrimSpace(*p.Contact)
	}
}

func connectError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// Discovery and CRUD remain generated stubs until their handlers are integrated.
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return connect.NewError(connect.CodeUnimplemented, errors.New("method not implemented"))
	}
	code := connect.CodeInternal
	message := "internal error"
	for _, entry := range []struct {
		err  error
		code connect.Code
	}{{app.ErrInvalidArgument, connect.CodeInvalidArgument}, {app.ErrUnauthenticated, connect.CodeUnauthenticated}, {app.ErrPermissionDenied, connect.CodePermissionDenied}, {app.ErrNotFound, connect.CodeNotFound}, {app.ErrFailedPrecondition, connect.CodeFailedPrecondition}, {app.ErrAlreadyExists, connect.CodeAlreadyExists}, {app.ErrAborted, connect.CodeAborted}} {
		if errors.Is(err, entry.err) {
			code = entry.code
			message = entry.err.Error()
			break
		}
	}
	if code == connect.CodeInternal {
		slog.ErrorContext(ctx, "Supplier workflow failed", "error", err)
	}
	return connect.NewError(code, errors.New(message))
}
func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(t.UTC())
}
func absolute(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	v := t.AsTime().UTC()
	return &v
}
func wallTime(t *timeofday.TimeOfDay) *int64 {
	if t == nil {
		return nil
	}
	v := int64(t.Hours)*3600000000 + int64(t.Minutes)*60000000 + int64(t.Seconds)*1000000 + int64(t.Nanos)/1000
	return &v
}
func wireWall(t *int64) *timeofday.TimeOfDay {
	if t == nil {
		return nil
	}
	n := *t
	return &timeofday.TimeOfDay{Hours: int32(n / 3600000000), Minutes: int32(n / 60000000 % 60), Seconds: int32(n / 1000000 % 60), Nanos: int32(n%1000000) * 1000}
}
func proposal(p *pb.LocationInput) app.Proposal {
	if p == nil {
		return app.Proposal{}
	}
	return app.Proposal{Name: p.Name, IsSupplier: p.GetIsSupplier(), CategoryIDs: p.CategoryIds, BuildingID: p.BuildingId, Floor: p.Floor, Latitude: p.GetCoordinates().GetLatitude(), Longitude: p.GetCoordinates().GetLongitude(), OpenFrom: wallTime(p.OpensAt), OpenTo: wallTime(p.ClosesAt), Contact: p.Contact, Details: p.Details}
}
func wireProposal(p app.Proposal) *pb.LocationInput {
	classification := p.IsSupplier
	out := &pb.LocationInput{Name: p.Name, IsSupplier: &classification, CategoryIds: p.CategoryIDs, BuildingId: p.BuildingID, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: wireWall(p.OpenFrom), ClosesAt: wireWall(p.OpenTo), Contact: p.Contact, Details: p.Details}
	if p.CoordinatesMissing {
		out.Coordinates = nil
	}
	return out
}

var stateFromWire = map[pb.DisablementState]app.DisablementState{pb.DisablementState_DISABLEMENT_STATE_UNSPECIFIED: "", pb.DisablementState_DISABLEMENT_STATE_SCHEDULED: app.Scheduled, pb.DisablementState_DISABLEMENT_STATE_ACTIVE: app.Active, pb.DisablementState_DISABLEMENT_STATE_ENDED: app.Ended, pb.DisablementState_DISABLEMENT_STATE_CANCELLED: app.Cancelled}
var stateToWire = map[app.DisablementState]pb.DisablementState{app.Scheduled: pb.DisablementState_DISABLEMENT_STATE_SCHEDULED, app.Active: pb.DisablementState_DISABLEMENT_STATE_ACTIVE, app.Ended: pb.DisablementState_DISABLEMENT_STATE_ENDED, app.Cancelled: pb.DisablementState_DISABLEMENT_STATE_CANCELLED}
var statusFromWire = map[pb.LocationAdditionRequestStatus]app.RequestStatus{pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_UNSPECIFIED: "", pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING: app.Pending, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED: app.Approved, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED: app.Rejected, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN: app.Withdrawn}
var statusToWire = map[app.RequestStatus]pb.LocationAdditionRequestStatus{app.Pending: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING, app.Approved: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED, app.Rejected: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED, app.Withdrawn: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN}

func (h *Handler) wireDisablement(d app.Disablement) *pb.Disablement {
	return &pb.Disablement{Id: d.ID, LocationId: d.LocationID, StartsAt: timestamp(&d.StartsAt), EndsAt: timestamp(d.EndsAt), EndedAt: timestamp(d.EndedAt), CancelledAt: timestamp(d.CancelledAt), Reason: d.Reason, CreatedBy: d.CreatedBy, State: stateToWire[d.State(h.clock())], Revision: d.Revision, CreatedAt: timestamp(&d.CreatedAt), UpdatedAt: timestamp(&d.UpdatedAt)}
}
func wireRequest(r app.AdditionRequest) *pb.LocationAdditionRequest {
	return &pb.LocationAdditionRequest{Id: r.ID, Proposal: wireProposal(r.Proposal), SubmittedBy: r.SubmittedBy, Status: statusToWire[r.Status], ReviewedBy: r.ReviewedBy, ReviewedAt: timestamp(r.ReviewedAt), ReviewNote: r.ReviewNote, ResultingLocationId: r.ResultingLocationID, Revision: r.Revision, CreatedAt: timestamp(&r.CreatedAt), UpdatedAt: timestamp(&r.UpdatedAt)}
}
func (h *Handler) wireLocation(l app.Location) *pb.Location {
	p := l.Proposal
	out := &pb.Location{Id: l.ID, Name: p.Name, IsSupplier: p.IsSupplier, Building: &pb.Building{Id: l.Building.ID, Name: l.Building.Name, Center: &pb.Coordinates{Latitude: l.Building.Latitude, Longitude: l.Building.Longitude}, RadiusM: l.Building.RadiusM, CreatedAt: timestamp(&l.Building.CreatedAt), UpdatedAt: timestamp(&l.Building.UpdatedAt)}, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: wireWall(p.OpenFrom), ClosesAt: wireWall(p.OpenTo), Contact: p.Contact, Details: p.Details, ArchivedAt: timestamp(l.ArchivedAt), Revision: l.Revision, CreatedAt: timestamp(&l.CreatedAt), UpdatedAt: timestamp(&l.UpdatedAt)}
	for _, c := range l.Categories {
		out.Categories = append(out.Categories, &pb.Category{Id: c.ID, Name: c.Name, CreatedAt: timestamp(&c.CreatedAt)})
	}
	if l.CurrentDisablement != nil {
		out.CurrentDisablement = h.wireDisablement(*l.CurrentDisablement)
	}
	return out
}
func wirePage(p app.PageInfo) *pb.PageInfo {
	return &pb.PageInfo{Page: p.Number, PageSize: p.Size, TotalItems: p.TotalItems, TotalPages: p.TotalPages}
}
func (h *Handler) CreateDisablement(ctx context.Context, r *connect.Request[pb.CreateDisablementRequest]) (*connect.Response[pb.CreateDisablementResponse], error) {
	d, e := h.operations.CreateDisablement(ctx, h.principal(ctx), app.CreateDisablement{LocationID: r.Msg.LocationId, StartsAt: absolute(r.Msg.StartsAt), EndsAt: absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Key: r.Msg.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CreateDisablementResponse{Disablement: h.wireDisablement(d)}), nil
}
func (h *Handler) ListDisablements(ctx context.Context, r *connect.Request[pb.ListDisablementsRequest]) (*connect.Response[pb.ListDisablementsResponse], error) {
	p, e := h.operations.ListDisablements(ctx, h.principal(ctx), r.Msg.LocationId, stateFromWire[r.Msg.State], app.Page{Number: r.Msg.Page, Size: r.Msg.PageSize})
	if e != nil {
		return nil, e
	}
	out := &pb.ListDisablementsResponse{PageInfo: wirePage(p.PageInfo)}
	for _, d := range p.Items {
		out.Disablements = append(out.Disablements, h.wireDisablement(d))
	}
	return connect.NewResponse(out), nil
}
func (h *Handler) UpdateDisablement(ctx context.Context, r *connect.Request[pb.UpdateDisablementRequest]) (*connect.Response[pb.UpdateDisablementResponse], error) {
	d, e := h.operations.UpdateDisablement(ctx, h.principal(ctx), app.UpdateDisablement{ID: r.Msg.Id, StartsAt: absolute(r.Msg.StartsAt), EndsAt: absolute(r.Msg.EndsAt), Reason: r.Msg.Reason, Paths: r.Msg.GetUpdateMask().GetPaths(), ExpectedRevision: r.Msg.ExpectedRevision})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.UpdateDisablementResponse{Disablement: h.wireDisablement(d)}), nil
}
func (h *Handler) EndDisablement(ctx context.Context, r *connect.Request[pb.EndDisablementRequest]) (*connect.Response[pb.EndDisablementResponse], error) {
	d, e := h.operations.EndDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.EndDisablementResponse{Disablement: h.wireDisablement(d)}), nil
}
func (h *Handler) CancelDisablement(ctx context.Context, r *connect.Request[pb.CancelDisablementRequest]) (*connect.Response[pb.CancelDisablementResponse], error) {
	d, e := h.operations.CancelDisablement(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.CancelDisablementResponse{Disablement: h.wireDisablement(d)}), nil
}
func (h *Handler) SubmitLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.SubmitLocationAdditionRequestRequest]) (*connect.Response[pb.SubmitLocationAdditionRequestResponse], error) {
	v, e := h.operations.SubmitRequest(ctx, h.principal(ctx), app.SubmitRequest{Proposal: proposal(r.Msg.Proposal), Key: r.Msg.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.SubmitLocationAdditionRequestResponse{Request: wireRequest(v)}), nil
}
func (h *Handler) GetLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.GetLocationAdditionRequestRequest]) (*connect.Response[pb.GetLocationAdditionRequestResponse], error) {
	v, e := h.operations.GetRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.GetLocationAdditionRequestResponse{Request: wireRequest(v)}), nil
}
func (h *Handler) ListLocationAdditionRequests(ctx context.Context, r *connect.Request[pb.ListLocationAdditionRequestsRequest]) (*connect.Response[pb.ListLocationAdditionRequestsResponse], error) {
	p, e := h.operations.ListRequests(ctx, h.principal(ctx), statusFromWire[r.Msg.Status], app.Page{Number: r.Msg.Page, Size: r.Msg.PageSize})
	if e != nil {
		return nil, e
	}
	out := &pb.ListLocationAdditionRequestsResponse{PageInfo: wirePage(p.PageInfo)}
	for _, v := range p.Items {
		out.Requests = append(out.Requests, wireRequest(v))
	}
	return connect.NewResponse(out), nil
}

// Translate API opening-hour names without changing the internal domain fields.
func proposalPaths(paths []string) []string {
	out := append([]string(nil), paths...)
	for i, path := range out {
		switch path {
		case "opens_at":
			out[i] = "open_from"
		case "closes_at":
			out[i] = "open_to"
		}
	}
	return out
}

func (h *Handler) UpdateLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.UpdateLocationAdditionRequestRequest]) (*connect.Response[pb.UpdateLocationAdditionRequestResponse], error) {
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
	return connect.NewResponse(&pb.UpdateLocationAdditionRequestResponse{Request: wireRequest(v)}), nil
}
func (h *Handler) WithdrawLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.WithdrawLocationAdditionRequestRequest]) (*connect.Response[pb.WithdrawLocationAdditionRequestResponse], error) {
	v, e := h.operations.WithdrawRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.WithdrawLocationAdditionRequestResponse{Request: wireRequest(v)}), nil
}
func (h *Handler) ApproveLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.ApproveLocationAdditionRequestRequest]) (*connect.Response[pb.ApproveLocationAdditionRequestResponse], error) {
	v, e := h.operations.ApproveRequest(ctx, h.principal(ctx), r.Msg.Id)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ApproveLocationAdditionRequestResponse{Request: wireRequest(v.Request), Location: h.wireLocation(v.Location)}), nil
}
func (h *Handler) RejectLocationAdditionRequest(ctx context.Context, r *connect.Request[pb.RejectLocationAdditionRequestRequest]) (*connect.Response[pb.RejectLocationAdditionRequestResponse], error) {
	v, e := h.operations.RejectRequest(ctx, h.principal(ctx), r.Msg.Id, r.Msg.ReviewNote)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.RejectLocationAdditionRequestResponse{Request: wireRequest(v)}), nil
}
