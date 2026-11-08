// Package accountemail implements the internal EmailService Connect handler.
package accountemail

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

var _ communicationv1connect.EmailServiceHandler = (*Handler)(nil)

// Handler sends account email for other services.
type Handler struct {
	communicationv1connect.UnimplementedEmailServiceHandler

	// Sender is nil when SMTP isn't configured.
	Sender email.EmailSender
	Log    *slog.Logger
}

// SendMagicLink emails a sign-in or sign-up link. The caller reports a failure
// to the user, so any delivery error is unavailable.
func (h *Handler) SendMagicLink(
	ctx context.Context,
	req *connect.Request[communicationv1.SendMagicLinkRequest],
) (*connect.Response[communicationv1.SendMagicLinkResponse], error) {
	if h.Sender == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("email is not configured"))
	}
	purpose := email.Login
	if req.Msg.GetPurpose() == communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_REGISTER {
		purpose = email.Register
	}
	if err := h.Sender.Send(ctx, email.MagicLink(req.Msg.GetEmail(), req.Msg.GetLink(), purpose)); err != nil {
		h.Log.WarnContext(ctx, "send magic link failed", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the email couldn't be sent"))
	}
	return connect.NewResponse(&communicationv1.SendMagicLinkResponse{}), nil
}
