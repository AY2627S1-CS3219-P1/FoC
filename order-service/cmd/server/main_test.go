package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

func TestServerServesConnectAndNativeGRPC(t *testing.T) {
	handler := newHandler(healthyDatabase{}, nil, func(next http.Handler) http.Handler { return next })
	server := newServer("127.0.0.1:0", handler)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shut down server: %v", err)
		}
		select {
		case err := <-serveErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("serve: %v", err)
			}
		case <-ctx.Done():
			t.Error("server did not stop")
		}
	})

	http1 := new(http.Protocols)
	http1.SetHTTP1(true)
	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)

	tests := []struct {
		name      string
		protocols *http.Protocols
		options   []connect.ClientOption
	}{
		{
			name:      "connect over HTTP/1.1",
			protocols: http1,
		},
		{
			name:      "native gRPC over unencrypted HTTP/2",
			protocols: h2c,
			options:   []connect.ClientOption{connect.WithGRPC()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &http.Transport{Protocols: test.protocols}
			defer transport.CloseIdleConnections()

			client := orderv1connect.NewHealthServiceClient(
				&http.Client{Transport: transport},
				"http://"+listener.Addr().String(),
				test.options...,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			response, err := client.Check(
				ctx,
				connect.NewRequest(&orderv1.CheckRequest{}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if response.Msg.Status != "ok" {
				t.Fatalf("expected status %q, got %q", "ok", response.Msg.Status)
			}
		})
	}
}

type healthyDatabase struct{}

func (healthyDatabase) Ping(context.Context) error { return nil }

func TestInternalParticipantServiceIsNotMounted(t *testing.T) {
	handler := newHandler(healthyDatabase{}, nil, func(next http.Handler) http.Handler { return next })
	request := httptest.NewRequest(http.MethodPost, "/order.v1.OrderInternalService/GetOrderParticipants", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("internal route returned %d", response.Code)
	}
}

func TestLocalFrontendCORS(t *testing.T) {
	handler := newHandler(healthyDatabase{}, nil, func(next http.Handler) http.Handler { return next })
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173", "http://localhost:5173.attacker.example"} {
		request := httptest.NewRequest(http.MethodOptions, "/order.v1.OrderService/GetOrder", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "authorization,connect-protocol-version,content-type")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		allowed := response.Header().Get("Access-Control-Allow-Origin")
		if strings.Contains(origin, "attacker") && allowed != "" {
			t.Fatalf("allowed attacker origin %s", allowed)
		}
		if !strings.Contains(origin, "attacker") && allowed != origin {
			t.Fatalf("origin %s: got %s", origin, allowed)
		}
	}
}
