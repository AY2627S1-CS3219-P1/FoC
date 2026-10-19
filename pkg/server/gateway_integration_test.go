package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

// TestMeshGateway exercises the deployed gateway, not a local test server.
// Opt in after bootstrap by setting RPC_SMOKE_BASE_URL and RPC_SMOKE_CA_FILE.
func TestMeshGateway(t *testing.T) {
	baseURL := os.Getenv("RPC_SMOKE_BASE_URL")
	if baseURL == "" {
		t.Skip("set RPC_SMOKE_BASE_URL and RPC_SMOKE_CA_FILE for deployed protocol checks")
	}
	certificate, err := os.ReadFile(os.Getenv("RPC_SMOKE_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("gateway CA file contains no certificates")
	}

	for _, grpc := range []bool{false, true} {
		t.Run(fmt.Sprintf("native-grpc=%t", grpc), func(t *testing.T) {
			protocols := new(http.Protocols)
			protocols.SetHTTP1(!grpc)
			protocols.SetHTTP2(grpc)
			transport := &http.Transport{
				Protocols:       protocols,
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			var options []connect.ClientOption
			if grpc {
				options = append(options, connect.WithGRPC())
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			user, err := userv1connect.NewHealthServiceClient(client, baseURL, options...).Check(ctx, connect.NewRequest(&userv1.CheckRequest{}))
			if err != nil || user.Msg.Status != "ok" {
				t.Fatalf("User health: %v, %v", user, err)
			}
			supplier, err := supplierv1connect.NewHealthServiceClient(client, baseURL, options...).Check(ctx, connect.NewRequest(&supplierv1.CheckRequest{}))
			if err != nil || supplier.Msg.Status != "ok" {
				t.Fatalf("Supplier health: %v, %v", supplier, err)
			}
			order, err := orderv1connect.NewHealthServiceClient(client, baseURL, options...).Check(ctx, connect.NewRequest(&orderv1.CheckRequest{}))
			if err != nil || order.Msg.Status != "ok" {
				t.Fatalf("Order health: %v, %v", order, err)
			}
		})
	}
}
