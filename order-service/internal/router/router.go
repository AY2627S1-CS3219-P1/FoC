// Package router mounts the order service's RPCs and their access policies.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

// RPCServices contains RPC servers constructed at the application composition root.
type RPCServices struct {
	Health orderv1connect.HealthServiceHandler
	Order  orderv1connect.OrderServiceHandler
	Admin  orderv1connect.OrderAdminServiceHandler
}

// Setup mounts the public health RPC and the protected order RPCs.
func Setup(services RPCServices, authenticate func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()

	healthPath, healthHandler := orderv1connect.NewHealthServiceHandler(services.Health)
	mux.Handle(healthPath, healthHandler)

	orderPath, orderHandler := orderv1connect.NewOrderServiceHandler(
		services.Order,
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()),
	)
	mux.Handle(orderPath, authenticate(orderHandler))

	adminPath, adminHandler := orderv1connect.NewOrderAdminServiceHandler(
		services.Admin,
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()),
	)
	mux.Handle(adminPath, authenticate(adminHandler))

	return mux
}

// SetupMesh leaves the participant API on the internal listener only. Its
// implementation remains deferred; generated unimplemented methods preserve
// the existing contract without inventing participant access policy here.
func SetupMesh(services RPCServices, authenticate func(http.Handler) http.Handler) (http.Handler, http.Handler) {
	public := Setup(services, authenticate)
	internal := http.NewServeMux()
	path, handler := orderv1connect.NewOrderInternalServiceHandler(
		&orderv1connect.UnimplementedOrderInternalServiceHandler{},
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	internal.Handle(path, handler)
	internal.Handle("/", public)
	return public, internal
}
