package rpc

import (
	"context"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

// HealthServer implements the generated user health service.
type HealthServer struct {
	userv1connect.UnimplementedHealthServiceHandler
}

// NewHealthServer creates a user health RPC server with no external dependencies.
func NewHealthServer() *HealthServer {
	return &HealthServer{}
}

// Check reports status "ok" for every request without probing service dependencies.
func (s *HealthServer) Check(
	_ context.Context,
	_ *connect.Request[userv1.CheckRequest],
) (*connect.Response[userv1.CheckResponse], error) {
	return connect.NewResponse(&userv1.CheckResponse{
		Status: "ok",
	}), nil
}
