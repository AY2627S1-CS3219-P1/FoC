package profile

import (
	"context"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

type Logic interface {
	GetMyProfile(context.Context, uint) (models.User, error)
	UpdateMyProfile(context.Context, uint, service.ProfileInput) (models.User, error)
}

type Handler struct {
	userv1connect.UnimplementedProfileServiceHandler
	Logic Logic
}

func (h *Handler) GetMyProfile(ctx context.Context, _ *connect.Request[userv1.GetMyProfileRequest]) (*connect.Response[userv1.GetMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	user, err := h.Logic.GetMyProfile(ctx, actorID)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	response := connect.NewResponse(&userv1.GetMyProfileResponse{Profile: userdto.Profile(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) UpdateMyProfile(ctx context.Context, req *connect.Request[userv1.UpdateMyProfileRequest]) (*connect.Response[userv1.UpdateMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	user, err := h.Logic.UpdateMyProfile(ctx, actorID, service.ProfileInput{
		DisplayName: req.Msg.DisplayName, Description: req.Msg.Description,
		TelegramHandle: req.Msg.TelegramHandle, PhoneNumber: req.Msg.PhoneNumber,
	})
	if err != nil {
		return nil, userdto.MapError(err)
	}
	response := connect.NewResponse(&userv1.UpdateMyProfileResponse{Profile: userdto.Profile(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}
