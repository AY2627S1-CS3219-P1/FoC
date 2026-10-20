package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	transport "github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/rpcauth"
)

type discoveryProbe struct {
	locationv1connect.UnimplementedLocationDiscoveryServiceHandler
	token string
	t     *testing.T
}

func (s *discoveryProbe) GetLocation(ctx context.Context, req *connect.Request[locationv1.GetLocationRequest]) (*connect.Response[locationv1.GetLocationResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok || caller.ID != "user-1" || req.Header().Get("Authorization") != "Bearer "+s.token {
		s.t.Error("authentication context or Authorization header lost")
	}
	return connect.NewResponse(&locationv1.GetLocationResponse{}), nil
}

func TestMeshHTTPSBrowserSameOriginRetainsJWT(t *testing.T) {
	t.Setenv("RPC_MODE", "mesh")
	authentication := rpcauth.New(t)
	token := authentication.Token(t, "user")
	services, err := newRPCServices(nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	services.Discovery = &discoveryProbe{token: token, t: t}
	public := getCorsConfig().Handler(router.Setup(&deps.Env{}, authentication.Authenticator, services))
	servers := (transport.Config{Mode: transport.Mesh, Port: "8080"}).Servers(public, nil)
	for _, listener := range servers {
		server := httptest.NewTLSServer(listener.Handler)
		client := locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL)
		for _, bearer := range []string{"", "invalid-jwt", token} {
			request := connect.NewRequest(&locationv1.GetLocationRequest{Id: "3bd7435a-201f-45d4-b858-c1081a93a63c"})
			request.Header().Set("Origin", server.URL)
			if bearer != "" {
				request.Header().Set("Authorization", "Bearer "+bearer)
			}
			_, err := client.GetLocation(context.Background(), request)
			if bearer == token {
				if err != nil {
					t.Fatalf("HTTPS same-origin request blocked: %v", err)
				}
			} else if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("invalid JWT accepted: %v", err)
			}
		}
		server.Close()
	}
}

func TestAuthenticationRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, err := retryAuthenticator(ctx, time.Hour, func(attempt context.Context) (*httpauth.Authenticator, error) {
		calls++
		if _, ok := attempt.Deadline(); !ok {
			t.Fatal("startup attempt has no deadline")
		}
		cancel()
		return nil, errors.New("not ready")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
