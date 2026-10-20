package health

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
)

// Return unimplemented for generated methods that HealthServer has not implemented
type HealthServer struct {
	supplierv1connect.UnimplementedHealthServiceHandler
	DB Pinger
}

type Pinger interface {
	Ping(context.Context) error
}

func NewServer(db Pinger) *HealthServer {
	return &HealthServer{DB: db}
}

// Check reports readiness only while the service database is reachable.
func (s *HealthServer) Check(
	ctx context.Context,
	_ *connect.Request[supplierv1.CheckRequest],
) (*connect.Response[supplierv1.CheckResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.DB == nil || s.DB.Ping(ctx) != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("database unavailable"))
	}
	return connect.NewResponse(&supplierv1.CheckResponse{
		Status: "ok",
	}), nil
}
