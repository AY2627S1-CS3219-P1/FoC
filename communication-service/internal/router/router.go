// Package router mounts the Communication Service's Connect handlers.
package router

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

// Services are the handlers the router mounts.
type Services struct {
	Health communicationv1connect.HealthServiceHandler
}

// New returns the HTTP handler for all Communication Service routes.
func New(services Services) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(communicationv1connect.NewHealthServiceHandler(services.Health))
	return mux
}
