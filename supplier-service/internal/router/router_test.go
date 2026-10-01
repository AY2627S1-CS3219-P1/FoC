package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
)

func TestLegacyAuthRoutesAreRemoved(t *testing.T) {
	router := Setup(&deps.Env{}, nil, nil)
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
