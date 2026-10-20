package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

// SetupSuspension mounts only behind the #29 workload authentication AND
// User-only authorization boundary. Neither user JWTs nor client-supplied
// identity headers satisfy that boundary. Default construction is fail-closed.
// Production composition does not call this until #29 is supplied; the public
// router never exposes this service. Tests inject temporary identities here.
func SetupSuspension(service orderv1connect.OrderInternalServiceHandler, authenticate func(http.Handler) http.Handler, authorize connect.Interceptor) http.Handler {
	if service == nil || authenticate == nil || authorize == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "authenticated User-to-Order boundary is not configured", http.StatusServiceUnavailable)
		})
	}
	path, handler := orderv1connect.NewOrderInternalServiceHandler(service, connect.WithInterceptors(authorize, validate.NewInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, authenticate(handler))
	return mux
}
