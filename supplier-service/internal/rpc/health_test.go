package rpc

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/foc/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/foc/supplier/v1/supplierv1connect"
	"github.com/go-chi/chi/v5"
)

func TestHealthService(t *testing.T) {
	path, handler := supplierv1connect.NewHealthServiceHandler(
		NewHealthServer(),
	)

	router := chi.NewRouter()
	router.Mount(path, handler)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := supplierv1connect.NewHealthServiceClient(
		server.Client(),
		server.URL,
	)
	response, err := client.Check(
		context.Background(),
		connect.NewRequest(&supplierv1.CheckRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if response.Msg.Status != "ok" {
		t.Fatalf("expected status %q, got %q", "ok", response.Msg.Status)
	}
}
