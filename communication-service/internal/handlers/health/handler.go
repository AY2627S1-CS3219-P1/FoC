// Package health implements the communication HealthService Connect handler.
package health

import (
	"context"

	"connectrpc.com/connect"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

var _ communicationv1connect.HealthServiceHandler = (*Handler)(nil)

// Handler reports that the service is up. Telegram is optional, so a missing
// bot token doesn't make the service unhealthy.
type Handler struct {
	communicationv1connect.UnimplementedHealthServiceHandler

	// TelegramEnabled reports whether the bot is currently polling.
	TelegramEnabled func() bool
}

// Check reports status "ok" and whether the Telegram bot is running.
func (h *Handler) Check(
	_ context.Context,
	_ *connect.Request[communicationv1.CheckRequest],
) (*connect.Response[communicationv1.CheckResponse], error) {
	return connect.NewResponse(&communicationv1.CheckResponse{
		Status:          "ok",
		TelegramEnabled: h.TelegramEnabled != nil && h.TelegramEnabled(),
	}), nil
}
