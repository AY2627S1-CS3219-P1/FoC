// Package health implements moderation.v1.HealthService.
package health

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	moderationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1/moderationv1connect"
)

const pingTimeout = 2 * time.Second

var _ moderationv1connect.HealthServiceHandler = (*Handler)(nil)

// Pinger is the database check the handler needs; *sql.DB satisfies it.
type Pinger interface {
	PingContext(context.Context) error
}

// Handler reports database availability.
type Handler struct {
	moderationv1connect.UnimplementedHealthServiceHandler

	DB Pinger
}

// Check reports status "ok" when the database answers, or Unavailable.
func (h *Handler) Check(
	ctx context.Context,
	_ *connect.Request[moderationv1.CheckRequest],
) (*connect.Response[moderationv1.CheckResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := h.DB.PingContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("database unavailable"))
	}
	return connect.NewResponse(&moderationv1.CheckResponse{Status: "ok"}), nil
}
