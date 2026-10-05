package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/health"
	discoveryrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/discovery"
	lifecyclerpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/lifecycle"
)

func TestLegacyAuthRoutesAreRemoved(t *testing.T) {
	router := Setup(&deps.Env{}, nil, RPCServices{
		Health:      healthrpc.NewHealthServer(),
		Discovery:   discoveryrpc.NewLocationServer(discovery.NewService(nil)),
		Admin:       lifecyclerpc.NewLocationAdminServer(nil),
		Disablement: &lifecyclerpc.WorkflowServer{}, AdditionRequest: &lifecyclerpc.WorkflowServer{},
		WorkflowInterceptor: auth.RequireCaller(),
	})
	for _, path := range []string{"/api/auth", "/api/auth/create", "/api/admin/auth/login"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("expected removed route to return 404, got %d", response.Code)
			}
		})
	}
}
