// Package health implements the communication HealthService Connect handler.
package health

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

var _ communicationv1connect.HealthServiceHandler = (*Handler)(nil)

type Pinger interface{ Ping(context.Context) error }

// Handler reports database availability. Telegram is optional, so a missing
// bot token does not make the service unhealthy.
type Handler struct {
	DB Pinger
	communicationv1connect.UnimplementedHealthServiceHandler

	// TelegramEnabled reports whether the bot is currently polling.
	TelegramEnabled func() bool
}

// Check reports status "ok" and whether the Telegram bot is running.
func (h *Handler) Check(
	ctx context.Context,
	_ *connect.Request[communicationv1.CheckRequest],
) (*connect.Response[communicationv1.CheckResponse], error) {
	if h.DB != nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := h.DB.Ping(ctx); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("database unavailable"))
		}
	}
	return connect.NewResponse(&communicationv1.CheckResponse{
		Status:          "ok",
		TelegramEnabled: h.TelegramEnabled != nil && h.TelegramEnabled(),
	}), nil
}
