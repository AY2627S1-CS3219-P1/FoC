package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

// Internal keeps the public RPC behavior on the internal listener and adds only
// suspension coordination. Istio STRICT mTLS authenticates workloads, and the
// order-suspension-user-only DENY policy restricts this method to User Service.
// Never mount this handler publicly or trust a client-supplied identity header.
func Internal(services RPCServices, authenticate func(http.Handler) http.Handler) http.Handler {
	mux := Setup(services, authenticate)
	if services.Suspension != nil {
		_, handler := orderv1connect.NewOrderInternalServiceHandler(services.Suspension,
			connect.WithInterceptors(validate.NewInterceptor()), connect.WithReadMaxBytes(1<<20))
		mux.Handle(orderv1connect.OrderInternalServiceApplyAccountSuspensionProcedure, handler)
	}
	return mux
}
