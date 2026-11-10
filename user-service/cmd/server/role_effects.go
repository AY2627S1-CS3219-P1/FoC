package main

import (
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

// No reachability check: User must start before Order can fetch its keys.
// Missing configuration leaves suspension disabled and unfinished work guarded.
func newOrderRoleEffects(baseURL string) *service.OrderRoleEffects {
	if baseURL == "" {
		return nil
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &service.OrderRoleEffects{Client: orderv1connect.NewOrderInternalServiceClient(client, baseURL)}
}
