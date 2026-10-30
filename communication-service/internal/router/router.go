// Package router mounts the Communication Service's Connect handlers.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

// Services are the handlers the router mounts.
type Services struct {
	Health       communicationv1connect.HealthServiceHandler
	TelegramLink communicationv1connect.TelegramLinkServiceHandler
}

// New returns the HTTP handler for all routes. Health is public; authenticate
// verifies User Service access tokens for everything else.
func New(services Services, authenticate func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(communicationv1connect.NewHealthServiceHandler(services.Health))

	linkPath, linkHandler := communicationv1connect.NewTelegramLinkServiceHandler(services.TelegramLink,
		connect.WithInterceptors(auth.RequireCaller()))
	mux.Handle(linkPath, authenticate(linkHandler))
	return mux
}

// Internal returns the reserved internal route table. Routes will be moved
// here when the service needs private endpoints.
func Internal() http.Handler { return http.NewServeMux() }
