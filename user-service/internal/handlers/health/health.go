package health

import (
	"context"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

var _ userv1connect.HealthServiceHandler = (*Handler)(nil)

// Handler implements the public health RPC without probing service dependencies.
type Handler struct {
	userv1connect.UnimplementedHealthServiceHandler
}

func New() *Handler {
	return &Handler{}
}

func (*Handler) Check(
	_ context.Context,
	_ *connect.Request[userv1.CheckRequest],
) (*connect.Response[userv1.CheckResponse], error) {
	return connect.NewResponse(&userv1.CheckResponse{Status: "ok"}), nil
}
