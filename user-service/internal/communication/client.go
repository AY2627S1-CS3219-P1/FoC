// Package communication sends magic links through Communication Service's
// internal EmailService.
package communication

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

var _ service.MagicLinkSender = (*MagicLinks)(nil)

// MagicLinks implements service.MagicLinkSender.
type MagicLinks struct {
	client communicationv1connect.EmailServiceClient
}

// NewMagicLinks calls the EmailService at baseURL, Communication's internal
// listener.
func NewMagicLinks(httpClient *http.Client, baseURL string) *MagicLinks {
	return &MagicLinks{client: communicationv1connect.NewEmailServiceClient(httpClient, baseURL)}
}

// SendMagicLink implements service.MagicLinkSender.
func (m *MagicLinks) SendMagicLink(ctx context.Context, to, link string, purpose service.MagicLinkPurpose) error {
	p := communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_LOGIN
	if purpose == service.MagicLinkRegister {
		p = communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_REGISTER
	}
	if _, err := m.client.SendMagicLink(ctx, connect.NewRequest(&communicationv1.SendMagicLinkRequest{
		Email: to, Link: link, Purpose: p,
	})); err != nil {
		return fmt.Errorf("send magic link: %w", err)
	}
	return nil
}
