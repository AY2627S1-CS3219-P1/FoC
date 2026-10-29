package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

type orderProbe struct {
	orderv1connect.UnimplementedOrderServiceHandler
	t *testing.T
}

func (s *orderProbe) GetOrder(ctx context.Context, req *connect.Request[orderv1.GetOrderRequest]) (*connect.Response[orderv1.GetOrderResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok || caller.ID != "user-1" || req.Header().Get("Authorization") != "Bearer existing-jwt" {
		s.t.Error("authentication context or Authorization header lost")
	}
	return connect.NewResponse(&orderv1.GetOrderResponse{}), nil
}

func TestHTTPSBrowserSameOriginRetainsJWT(t *testing.T) {
	services := testRPCServices()
	services.Order = &orderProbe{t: t}
	authenticate := httpauth.AuthenticateLocal(func(token string) (auth.Caller, error) {
		if token != "existing-jwt" {
			return auth.Caller{}, errors.New("invalid token")
		}
		return auth.Caller{ID: "user-1", Role: "user"}, nil
	})
	handler := getCorsConfig().Handler(router.Setup(services, authenticate))
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client := orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
	for _, bearer := range []string{"", "invalid-jwt", "existing-jwt"} {
		request := connect.NewRequest(&orderv1.GetOrderRequest{Id: "3bd7435a-201f-45d4-b858-c1081a93a63c"})
		request.Header().Set("Origin", server.URL)
		if bearer != "" {
			request.Header().Set("Authorization", "Bearer "+bearer)
		}
		_, err := client.GetOrder(context.Background(), request)
		if bearer == "existing-jwt" {
			if err != nil {
				t.Fatalf("HTTPS same-origin request blocked: %v", err)
			}
		} else if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("missing/invalid JWT accepted: %v", err)
		}
	}
}
