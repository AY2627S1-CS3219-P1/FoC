// Package telegramlink implements the TelegramLinkService Connect handler.
package telegramlink

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ communicationv1connect.TelegramLinkServiceHandler = (*Handler)(nil)

// Logic is the part of the linking service the handler needs.
type Logic interface {
	CreateCode(ctx context.Context, userID uuid.UUID) (string, time.Time, error)
	Get(ctx context.Context, userID uuid.UUID) (linking.Link, error)
	Unlink(ctx context.Context, userID uuid.UUID) error
}

// Handler adapts the linking service to the generated Connect API.
type Handler struct {
	communicationv1connect.UnimplementedTelegramLinkServiceHandler

	Logic Logic
	// BotUsername returns the running bot's username, or "" while Telegram
	// is disabled.
	BotUsername func() string
}

// CreateTelegramLink returns a one-time t.me link for the caller.
func (h *Handler) CreateTelegramLink(
	ctx context.Context,
	_ *connect.Request[communicationv1.CreateTelegramLinkRequest],
) (*connect.Response[communicationv1.CreateTelegramLinkResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	bot := h.BotUsername()
	if bot == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("telegram is unavailable"))
	}
	code, expiresAt, err := h.Logic.CreateCode(ctx, userID)
	if err != nil {
		return nil, internal(ctx, err)
	}
	link := url.URL{Scheme: "https", Host: "t.me", Path: "/" + bot, RawQuery: url.Values{"start": {code}}.Encode()}
	return connect.NewResponse(&communicationv1.CreateTelegramLinkResponse{
		LinkUrl:   link.String(),
		ExpiresAt: timestamppb.New(expiresAt),
	}), nil
}

// GetTelegramLink reports whether the caller is linked.
func (h *Handler) GetTelegramLink(
	ctx context.Context,
	_ *connect.Request[communicationv1.GetTelegramLinkRequest],
) (*connect.Response[communicationv1.GetTelegramLinkResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	link, err := h.Logic.Get(ctx, userID)
	if errors.Is(err, linking.ErrNotLinked) {
		return connect.NewResponse(&communicationv1.GetTelegramLinkResponse{}), nil
	}
	if err != nil {
		return nil, internal(ctx, err)
	}
	resp := &communicationv1.GetTelegramLinkResponse{Linked: true, LinkedAt: timestamppb.New(link.LinkedAt)}
	if link.Username != "" {
		resp.TelegramUsername = &link.Username
	}
	return connect.NewResponse(resp), nil
}

// DeleteTelegramLink unlinks the caller. It succeeds when nothing is linked.
func (h *Handler) DeleteTelegramLink(
	ctx context.Context,
	_ *connect.Request[communicationv1.DeleteTelegramLinkRequest],
) (*connect.Response[communicationv1.DeleteTelegramLinkResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.Logic.Unlink(ctx, userID); err != nil {
		return nil, internal(ctx, err)
	}
	return connect.NewResponse(&communicationv1.DeleteTelegramLinkResponse{}), nil
}

func callerID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	id, err := uuid.Parse(caller.ID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	return id, nil
}

// internal logs the cause and hides storage details from the client.
func internal(ctx context.Context, err error) error {
	slog.ErrorContext(ctx, "telegram link failed", "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
