// Package router mounts the Communication Service's Connect handlers.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

// Services are the handlers the router mounts.
type Services struct {
	Health       communicationv1connect.HealthServiceHandler
	TelegramLink communicationv1connect.TelegramLinkServiceHandler
	Email        communicationv1connect.EmailServiceHandler
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

// Internal returns the handler for the internal listener, which only other
// services reach. It serves health and account email, without user tokens.
func Internal(services Services) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(communicationv1connect.NewHealthServiceHandler(services.Health))
	mux.Handle(communicationv1connect.NewEmailServiceHandler(services.Email,
		connect.WithInterceptors(validate.NewInterceptor())))
	return mux
}
