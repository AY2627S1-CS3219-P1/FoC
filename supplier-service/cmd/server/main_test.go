package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	supplierrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc"
)

func TestServerServesConnectAndNativeGRPC(t *testing.T) {
	path, handler := supplierv1connect.NewHealthServiceHandler(
		supplierrpc.NewHealthServer(),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := newServer("127.0.0.1:0", mux)
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

			client := supplierv1connect.NewHealthServiceClient(
				&http.Client{Transport: transport},
				"http://"+listener.Addr().String(),
				test.options...,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			response, err := client.Check(
				ctx,
				connect.NewRequest(&supplierv1.CheckRequest{}),
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
