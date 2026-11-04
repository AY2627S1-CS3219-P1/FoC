// Package router mounts Credit's RPCs on its two listeners.
//
// The public listener serves users, authenticated by their User Service JWT.
// The internal listener serves Order's reservation calls. It does not verify
// Order's workload JWT yet (#29), so it must only be reachable from inside the
// service network; see LANDMINES/credit-internal-listener-unauthenticated.md.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

// RPCServices contains RPC servers constructed at the composition root.
type RPCServices struct {
	Health      grpchealth.Checker
	Account     creditv1connect.CreditAccountServiceHandler
	Admin       creditv1connect.CreditAdminServiceHandler
	Reservation creditv1connect.CreditReservationServiceHandler
}

// Public mounts the health check, the caller's account RPCs and the
// administrator RPCs.
func Public(services RPCServices, authenticate func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))

	accountPath, accountHandler := creditv1connect.NewCreditAccountServiceHandler(
		services.Account,
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()),
	)
	mux.Handle(accountPath, authenticate(accountHandler))

	adminPath, adminHandler := creditv1connect.NewCreditAdminServiceHandler(
		services.Admin,
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()),
	)
	mux.Handle(adminPath, authenticate(adminHandler))

	return mux
}

// Internal mounts the health check and Order's reservation RPCs.
func Internal(services RPCServices) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))

	reservationPath, reservationHandler := creditv1connect.NewCreditReservationServiceHandler(
		services.Reservation,
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	mux.Handle(reservationPath, reservationHandler)

	return mux
}
