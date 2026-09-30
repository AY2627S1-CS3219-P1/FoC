// Package health implements the user HealthService Connect handler.
package health

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

const pingTimeout = 2 * time.Second

var _ userv1connect.HealthServiceHandler = (*Handler)(nil)

type Pinger interface {
	PingContext(context.Context) error
}

type Handler struct {
	userv1connect.UnimplementedHealthServiceHandler

	DB Pinger
}

func (h *Handler) Check(
	ctx context.Context,
	_ *connect.Request[userv1.CheckRequest],
) (*connect.Response[userv1.CheckResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := h.DB.PingContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("database unavailable"))
	}
	return connect.NewResponse(&userv1.CheckResponse{
		Status: "ok",
	}), nil
}
