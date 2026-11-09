package router_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/grpchealth"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/router"
	reviewv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1/reviewv1connect"
	moderationv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1/moderationv1connect"
)

func TestPublicStandardHealthRPC(t *testing.T) {
	mux := router.Public(router.RPCServices{
		Health:                grpchealth.NewStaticChecker(),
		ReviewModeration:      reviewv1connect.UnimplementedReviewModerationServiceHandler{},
		ReviewModerationAdmin: reviewv1connect.UnimplementedReviewModerationAdminServiceHandler{},
		ModerationAdmin:       moderationv1connect.UnimplementedModerationAdminServiceHandler{},
	}, func(next http.Handler) http.Handler {
		return next
	})
	assertHealthServing(t, mux)

	response := httptest.NewRecorder()
	request := healthRequest(t, "/moderation.v1.HealthService/Check")
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy health route status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestInternalStandardHealthRPC(t *testing.T) {
	mux := router.Internal(router.RPCServices{Health: grpchealth.NewStaticChecker()})
	assertHealthServing(t, mux)
}

func assertHealthServing(t *testing.T, mux http.Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, healthRequest(t, "/grpc.health.v1.Health/Check"))
	if response.Code != http.StatusOK {
		t.Fatalf("health route status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct{ Status string }
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode standard health response: %v", err)
	}
	if body.Status != "SERVING_STATUS_SERVING" {
		t.Fatalf("health status = %q, want SERVING_STATUS_SERVING", body.Status)
	}
}

func healthRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	return request
}
