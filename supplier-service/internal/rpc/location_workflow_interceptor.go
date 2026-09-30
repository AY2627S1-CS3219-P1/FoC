package rpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"google.golang.org/protobuf/proto"
)

func (h *WorkflowServer) interceptor(validator protovalidate.Validator) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c := h.principal(ctx)
			if !c.Authenticated() {
				return nil, connectError(ctx, app.ErrUnauthenticated)
			}
			procedure := req.Spec().Procedure
			adminOnly := false
			switch procedure {
			case locationv1connect.LocationAdminServiceCreateLocationProcedure,
				locationv1connect.LocationAdminServiceUpdateLocationProcedure,
				locationv1connect.LocationAdminServiceArchiveLocationProcedure,
				locationv1connect.LocationAdminServiceUnarchiveLocationProcedure,
				locationv1connect.LocationDisablementServiceCreateDisablementProcedure,
				locationv1connect.LocationDisablementServiceListDisablementsProcedure,
				locationv1connect.LocationDisablementServiceUpdateDisablementProcedure,
				locationv1connect.LocationDisablementServiceEndDisablementProcedure,
				locationv1connect.LocationDisablementServiceCancelDisablementProcedure,
				locationv1connect.LocationAdditionRequestServiceApproveLocationAdditionRequestProcedure,
				locationv1connect.LocationAdditionRequestServiceRejectLocationAdditionRequestProcedure:
				adminOnly = true
			}
			if adminOnly && !c.IsAdmin() {
				return nil, connectError(ctx, app.ErrPermissionDenied)
			}
			if procedure == locationv1connect.LocationAdditionRequestServiceSubmitLocationAdditionRequestProcedure && c.Role == "suspended_user" {
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
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
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
