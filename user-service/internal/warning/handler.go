package warning

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WarningLogic interface {
	ListWarnings(context.Context, uuid.UUID) ([]Warning, error)
}

type Handler struct {
	userv1connect.UnimplementedWarningServiceHandler
	Logic WarningLogic
}

func (h *Handler) GetMyWarnings(
	ctx context.Context,
	_ *connect.Request[userv1.GetMyWarningsRequest],
) (*connect.Response[userv1.GetMyWarningsResponse], error) {
	userID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	warnings, err := h.Logic.ListWarnings(ctx, userID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.GetMyWarningsResponse{Warnings: warningDTOs(warnings)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) GetWarningsForUser(
	ctx context.Context,
	req *connect.Request[userv1.GetWarningsForUserRequest],
) (*connect.Response[userv1.GetWarningsForUserResponse], error) {
	if _, err := userdto.ActorID(ctx); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	userID, err := uuid.Parse(req.Msg.UserId)
	if err != nil || userID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	warnings, err := h.Logic.ListWarnings(ctx, userID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.GetWarningsForUserResponse{Warnings: warningDTOs(warnings)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func warningDTOs(warnings []Warning) []*userv1.AccountWarning {
	result := make([]*userv1.AccountWarning, len(warnings))
	for i, warning := range warnings {
		item := &userv1.AccountWarning{
			Id: warning.ID.String(), RequestId: warning.RequestID.String(), Reason: warning.Reason,
			Status: warningStatus(warning.Status), CreatedAt: timestamppb.New(warning.CreatedAt),
		}
		if warning.RemovedAt != nil {
			item.RemovedAt = timestamppb.New(*warning.RemovedAt)
		}
		if warning.RemovedReason != nil {
			item.RemovedReason = warning.RemovedReason
		}
		result[i] = item
	}
	return result
}

func warningStatus(status Status) userv1.WarningStatus {
	switch status {
	case StatusActive:
		return userv1.WarningStatus_WARNING_STATUS_ACTIVE
	case StatusRemoved:
		return userv1.WarningStatus_WARNING_STATUS_REMOVED
	default:
		return userv1.WarningStatus_WARNING_STATUS_UNSPECIFIED
	}
}
