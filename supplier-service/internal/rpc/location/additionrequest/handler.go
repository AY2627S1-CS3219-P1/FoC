package additionrequest

import (
	"context"
	"errors"
	"net/http"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

type Operations interface {
	SubmitRequest(context.Context, app.Caller, app.SubmitRequest) (app.AdditionRequest, error)
	ListRequests(context.Context, app.Caller, app.RequestStatus, app.Page) (app.RequestPage, error)
	GetRequest(context.Context, app.Caller, string) (app.AdditionRequest, error)
	UpdateRequest(context.Context, app.Caller, app.UpdateRequest) (app.AdditionRequest, error)
	WithdrawRequest(context.Context, app.Caller, string) (app.AdditionRequest, error)
	ApproveRequest(context.Context, app.Caller, string) (app.Approval, error)
	RejectRequest(context.Context, app.Caller, string, string) (app.AdditionRequest, error)
}

type Principal = rpcshared.Principal
type Server struct {
	locationv1connect.UnimplementedLocationAdditionRequestServiceHandler
	operations Operations
	principal  Principal
	clock      func() time.Time
}

func NewServer(operations Operations, principal Principal, clock func() time.Time) *Server {
	if operations == nil || principal == nil || clock == nil {
		panic("Location handler dependencies are required")
	}
	return &Server{operations: operations, principal: principal, clock: clock}
}
func Mount(mux *http.ServeMux, handler *Server, authenticate func(http.Handler) http.Handler) error {
	if authenticate == nil {
		return errors.New("Location JWT authentication boundary is required")
	}
	validator, err := protovalidate.New()
	if err != nil {
		return err
	}
	path, h := locationv1connect.NewLocationAdditionRequestServiceHandler(handler, connect.WithInterceptors(CallerInterceptor(handler.principal, validator)))
	mux.Handle(path, authenticate(h))
	return nil
}
