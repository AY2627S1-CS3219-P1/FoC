package realtime

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

type Handler struct {
	communicationv1connect.UnimplementedRealtimeServiceHandler
	Hub *Hub
}

func (h *Handler) WatchChanges(
	ctx context.Context,
	_ *connect.Request[pb.WatchChangesRequest],
	stream *connect.ServerStream[pb.WatchChangesResponse],
) error {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	}
	if !caller.ExpiresAt.IsZero() && !caller.ExpiresAt.After(time.Now()) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("access token expired"))
	}
	s := h.Hub.subscribe(caller)
	if s == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("live updates unavailable"))
	}
	defer h.Hub.remove(s)
	if err := stream.Send(&pb.WatchChangesResponse{Ready: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	var expires <-chan time.Time
	if !caller.ExpiresAt.IsZero() {
		timer := time.NewTimer(time.Until(caller.ExpiresAt))
		defer timer.Stop()
		expires = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return connect.NewError(connect.CodeUnavailable, errors.New("live updates reconnecting"))
		case <-expires:
			return connect.NewError(connect.CodeUnauthenticated, errors.New("access token expired"))
		case <-ticker.C:
			if err := stream.Send(&pb.WatchChangesResponse{Heartbeat: true}); err != nil {
				return err
			}
		case <-s.wake:
			if err := stream.Send(&pb.WatchChangesResponse{Scopes: h.Hub.take(s)}); err != nil {
				return err
			}
		}
	}
}
