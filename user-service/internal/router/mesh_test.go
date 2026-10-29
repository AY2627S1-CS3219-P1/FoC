package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
)

func TestRouteGroupsKeepKeysPrivateAndPreserveAllRoutesHandler(t *testing.T) {
	authCalls := 0
	public, internal, allRoutes := router.SetupRoutes(testHealth(), &authhandler.Handler{Logic: &stubLogic{}, AllowedOrigin: frontendOrigin}, router.ProtectedRoutes{
		Profile: &profilehandler.Handler{},
		Admin:   &adminhandler.Handler{},
		Authenticate: func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				authCalls++
				if request.Header.Get("Authorization") != "Bearer existing-token" {
					t.Error("delegation lost Authorization header")
				}
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
			})
		},
	})
	for name, handler := range map[string]http.Handler{"public": public, "internal": internal} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx := context.Background()
			keys := userv1connect.NewPublicKeyServiceClient(server.Client(), server.URL)
			response, err := keys.GetPublicKeys(ctx, connect.NewRequest(&userv1.GetPublicKeysRequest{}))
			if name == "public" {
				if connect.CodeOf(err) != connect.CodeUnimplemented {
					t.Fatalf("public keys exposed: %v", err)
				}
			} else if err != nil || len(response.Msg.Keys) != 1 {
				t.Fatalf("internal keys unavailable: %v", err)
			}
			health := userv1connect.NewHealthServiceClient(server.Client(), server.URL)
			if _, err := health.Check(ctx, connect.NewRequest(&userv1.CheckRequest{})); err != nil {
				t.Fatal(err)
			}
			profile := userv1connect.NewProfileServiceClient(server.Client(), server.URL)
			request := connect.NewRequest(&userv1.GetMyProfileRequest{})
			request.Header().Set("Authorization", "Bearer existing-token")
			if _, err := profile.GetMyProfile(ctx, request); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("protected route lost authentication: %v", err)
			}
		})
	}
	allServer := httptest.NewServer(allRoutes)
	defer allServer.Close()
	allKeys := userv1connect.NewPublicKeyServiceClient(allServer.Client(), allServer.URL)
	if response, err := allKeys.GetPublicKeys(context.Background(), connect.NewRequest(&userv1.GetPublicKeysRequest{})); err != nil || len(response.Msg.Keys) != 1 {
		t.Fatalf("single-listener route set omitted public keys: %v", err)
	}
	if authCalls != 2 {
		t.Fatalf("authentication called %d times, expected once per listener", authCalls)
	}
}
