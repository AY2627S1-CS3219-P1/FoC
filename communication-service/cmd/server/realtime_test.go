package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/realtime"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

func TestRealtimeProductionHTTP1AndGRPCOverH2C(t *testing.T) {
	hub := realtime.NewHub()
	hub.SetReady(true)
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithCaller(
				r.Context(),
				auth.Caller{ID: "transport-user", ExpiresAt: time.Now().Add(time.Minute)},
			)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handler := corsConfig().Handler(router.New(router.Services{
		Health:       &communicationv1connect.UnimplementedHealthServiceHandler{},
		TelegramLink: &communicationv1connect.UnimplementedTelegramLinkServiceHandler{},
		Realtime:     &realtime.Handler{Hub: hub},
	}, authenticate))
	server := httptest.NewUnstartedServer(handler)
	server.Config = newServer("", handler)
	server.Start()
	defer server.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}
	defer transport.CloseIdleConnections()
	for _, tc := range []struct {
		name    string
		client  *http.Client
		options []connect.ClientOption
	}{
		{"connect-http1", server.Client(), []connect.ClientOption{connect.WithProtoJSON()}},
		{"grpc-h2c", &http.Client{Transport: transport}, []connect.ClientOption{connect.WithGRPC()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client := communicationv1connect.NewRealtimeServiceClient(tc.client, server.URL, tc.options...)
			request := connect.NewRequest(&pb.WatchChangesRequest{})
			request.Header().Set("Origin", "http://localhost:5173")
			stream, err := client.WatchChanges(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close() //nolint:errcheck
			if !stream.Receive() || !stream.Msg().Ready {
				t.Fatalf("ready was not flushed: %v", stream.Err())
			}
			if tc.name == "grpc-h2c" {
				// An HTTP/2 write deadline also resets an idle stream. The
				// per-write timeout must be cleared before the next heartbeat.
				timer := time.NewTimer(11 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					t.Fatal(ctx.Err())
				case <-timer.C:
				}
			}
			hub.Notify(
				realtime.Notice{
					UserID: "transport-user",
					Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS},
				},
			)
			if !stream.Receive() || len(stream.Msg().Scopes) != 1 {
				t.Fatalf("change was not flushed: %v", stream.Err())
			}
		})
	}
}
