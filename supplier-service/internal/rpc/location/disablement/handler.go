package disablement

import (
	"context"
	"errors"
	"net/http"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

type Operations interface {
	CreateDisablement(context.Context, app.Caller, app.CreateDisablement) (app.Disablement, error)
	ListDisablements(context.Context, app.Caller, string, app.DisablementState, app.Page) (app.DisablementPage, error)
	UpdateDisablement(context.Context, app.Caller, app.UpdateDisablement) (app.Disablement, error)
	EndDisablement(context.Context, app.Caller, string) (app.Disablement, error)
	CancelDisablement(context.Context, app.Caller, string) (app.Disablement, error)
}

type Principal = rpcshared.Principal
type Server struct {
	locationv1connect.UnimplementedLocationDisablementServiceHandler
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
	path, h := locationv1connect.NewLocationDisablementServiceHandler(handler, connect.WithInterceptors(CallerInterceptor(handler.principal, validator)))
	mux.Handle(path, authenticate(h))
	return nil
}
