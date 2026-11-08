package profile

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Logic interface {
	GetMyProfile(context.Context, uuid.UUID) (models.User, error)
	UpdateMyProfile(context.Context, uuid.UUID, service.ProfileInput) (models.User, error)
	GetMyAccountDeletionStatus(context.Context, uuid.UUID) (service.AccountDeletionStatus, error)
}

type Handler struct {
	userv1connect.UnimplementedProfileServiceHandler
	Logic Logic
}

func (h *Handler) GetMyProfile(
	ctx context.Context,
	_ *connect.Request[userv1.GetMyProfileRequest],
) (*connect.Response[userv1.GetMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	user, err := h.Logic.GetMyProfile(ctx, actorID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.GetMyProfileResponse{Profile: userdto.Profile(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) UpdateMyProfile(
	ctx context.Context,
	req *connect.Request[userv1.UpdateMyProfileRequest],
) (*connect.Response[userv1.UpdateMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	user, err := h.Logic.UpdateMyProfile(ctx, actorID, service.ProfileInput{
		DisplayName: req.Msg.DisplayName, Description: req.Msg.Description,
		PhoneNumber: req.Msg.PhoneNumber,
	})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.UpdateMyProfileResponse{Profile: userdto.Profile(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) GetMyAccountDeletionStatus(
	ctx context.Context,
	_ *connect.Request[userv1.GetMyAccountDeletionStatusRequest],
) (*connect.Response[userv1.GetMyAccountDeletionStatusResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	status, err := h.Logic.GetMyAccountDeletionStatus(ctx, actorID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := &userv1.GetMyAccountDeletionStatusResponse{
		IsDeleting:     status.IsDeleting,
		WaitingReasons: status.WaitingReasons,
	}
	if status.RequestedAt != nil {
		response.RequestedAt = timestamppb.New(*status.RequestedAt)
	}
	if status.LastCheckedAt != nil {
		response.LastCheckedAt = timestamppb.New(*status.LastCheckedAt)
	}
	result := connect.NewResponse(response)
	result.Header().Set("Cache-Control", "no-store")
	return result, nil
}
