// Package health implements the user health service.
package health

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

var _ userv1connect.HealthServiceHandler = (*Handler)(nil)

// Pinger is the database check the handler needs; *sql.DB satisfies it.
type Pinger interface {
	PingContext(context.Context) error
}

// Handler adapts the database check to the generated Connect API.
type Handler struct {
	userv1connect.UnimplementedHealthServiceHandler

	DB Pinger
}

// Check reports status "ok" when the database ping succeeds, or
// CodeUnavailable when it fails.
func (h *Handler) Check(
	ctx context.Context,
	_ *connect.Request[userv1.CheckRequest],
) (*connect.Response[userv1.CheckResponse], error) {
	if err := h.DB.PingContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("database unavailable"))
	}
	return connect.NewResponse(&userv1.CheckResponse{
		Status: "ok",
	}), nil
}
