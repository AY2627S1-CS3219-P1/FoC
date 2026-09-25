package rpc

import (
	"context"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/foc/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/foc/supplier/v1/supplierv1connect"
)

// Return unimplemented for generated methods that HealthServer has not implemented
type HealthServer struct {
	supplierv1connect.UnimplementedHealthServiceHandler
}

// Constructor
func NewHealthServer() *HealthServer {
	return &HealthServer{}
}

// Add Check method to HealthServer
func (s *HealthServer) Check(
	_ context.Context,
	_ *connect.Request[supplierv1.CheckRequest],
) (*connect.Response[supplierv1.CheckResponse], error) {
	return connect.NewResponse(&supplierv1.CheckResponse{
		Status: "ok",
	}), nil
}
