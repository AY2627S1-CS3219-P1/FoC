// Package router mounts the Communication Service's Connect handlers.
package router

import (
	"net/http"
	"time"

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
	Realtime     communicationv1connect.RealtimeServiceHandler
}

// New returns the HTTP handler for all routes. Health is public; authenticate
// verifies User Service access tokens for everything else.
func New(services Services, authenticate func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(communicationv1connect.NewHealthServiceHandler(services.Health))

	linkPath, linkHandler := communicationv1connect.NewTelegramLinkServiceHandler(services.TelegramLink,
		connect.WithInterceptors(auth.RequireCaller()))
	mux.Handle(linkPath, authenticate(linkHandler))
	if services.Realtime != nil {
		path, handler := communicationv1connect.NewRealtimeServiceHandler(
			services.Realtime,
			connect.WithInterceptors(auth.RequireCaller()),
		)
		mux.Handle(
			path,
			authenticate(
				http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(streamWriter{w}, r) },
				),
			),
		)
	}
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

// Per-write deadlines bound stalled clients without imposing a lifetime limit.
type streamWriter struct{ http.ResponseWriter }

func (w streamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w streamWriter) Flush() {
	controller := http.NewResponseController(w.ResponseWriter)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	_ = controller.Flush()
}

func (w streamWriter) Write(data []byte) (int, error) {
	controller := http.NewResponseController(w.ResponseWriter)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	return w.ResponseWriter.Write(data)
}
