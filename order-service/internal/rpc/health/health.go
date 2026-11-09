package health

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

// Database lists only the dependency health consumes.
type Database interface{ Ping(context.Context) error }

type Server struct {
	orderv1connect.UnimplementedHealthServiceHandler
	DB Database
}

func (s *Server) Check(
	ctx context.Context,
	_ *connect.Request[orderv1.CheckRequest],
) (*connect.Response[orderv1.CheckResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&orderv1.CheckResponse{Status: "ok"}), nil
}
