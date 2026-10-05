package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

// WorkflowOperations is the application boundary consumed by this transport.
type WorkflowOperations interface {
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
type Principal = rpcshared.Principal

// WorkflowServer implements Disablement and Location addition request operations.
type WorkflowServer struct {
	locationv1connect.UnimplementedLocationDisablementServiceHandler
	locationv1connect.UnimplementedLocationAdditionRequestServiceHandler
	operations WorkflowOperations
	principal  Principal
	clock      func() time.Time
}

func NewWorkflowServer(operations WorkflowOperations, principal Principal, clock func() time.Time) *WorkflowServer {
	if operations == nil || principal == nil || clock == nil {
		panic("workflow handler dependencies are required")
	}
	return &WorkflowServer{operations: operations, principal: principal, clock: clock}
}

// MountWorkflowServices requires an HTTP authentication boundary. The production composition
// root must supply JWT middleware and a Principal accessor before mounting.
// No unauthenticated/default-identity production fallback is provided.
func MountWorkflowServices(mux *http.ServeMux, handler *WorkflowServer, authenticate func(http.Handler) http.Handler) error {
	if authenticate == nil {
		return errors.New("workflow JWT authentication boundary is required")
	}
	validator, err := protovalidate.New()
	if err != nil {
		return err
	}
	options := []connect.HandlerOption{connect.WithInterceptors(rpcshared.WorkflowInterceptor(handler.principal, validator))}
	path, h := locationv1connect.NewLocationDisablementServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	path, h = locationv1connect.NewLocationAdditionRequestServiceHandler(handler, options...)
	mux.Handle(path, authenticate(h))
	return nil
}
