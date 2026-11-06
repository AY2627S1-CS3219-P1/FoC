package warning

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WarningLogic interface {
	ListWarnings(context.Context, uuid.UUID, pagination.Pagination) (WarningPage, error)
	MarkWarningAsRemoved(context.Context, uuid.UUID, *string, *uuid.UUID, uuid.UUID) error
}

type Handler struct {
	userv1connect.UnimplementedWarningServiceHandler
	Logic WarningLogic
}

func (h *Handler) ListMyWarnings(
	ctx context.Context,
	req *connect.Request[userv1.ListMyWarningsRequest],
) (*connect.Response[userv1.ListMyWarningsResponse], error) {
	userID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	warnings, err := h.Logic.ListWarnings(ctx, userID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(toListMyWarningsResponse(warnings))
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) ListWarningsForUser(
	ctx context.Context,
	req *connect.Request[userv1.ListWarningsForUserRequest],
) (*connect.Response[userv1.ListWarningsForUserResponse], error) {
	if _, err := userdto.ActorID(ctx); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	userID, err := uuid.Parse(req.Msg.UserId)
	if err != nil || userID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	warnings, err := h.Logic.ListWarnings(ctx, userID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(toListWarningsForUserResponse(warnings))
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) MarkWarningAsRemoved(
	ctx context.Context,
	req *connect.Request[userv1.MarkWarningAsRemovedRequest],
) (*connect.Response[userv1.MarkWarningAsRemovedResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	warningID, err := uuid.Parse(req.Msg.GetId())
	if err != nil || warningID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid warning ID"))
	}

	var appealID *uuid.UUID
	if req.Msg.AppealId != nil {
		parsed, err := uuid.Parse(req.Msg.GetAppealId())
		if err != nil || parsed == uuid.Nil {
			return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid appeal ID"))
		}
		appealID = &parsed
	}

	var removedReason *string
	if req.Msg.RemovedReason != nil {
		value := req.Msg.GetRemovedReason()
		removedReason = &value
	}
	if err := h.Logic.MarkWarningAsRemoved(ctx, warningID, removedReason, appealID, actorID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&userv1.MarkWarningAsRemovedResponse{}), nil
}

func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(p.GetPage(), p.GetPageSize())
}

func toListMyWarningsResponse(page WarningPage) *userv1.ListMyWarningsResponse {
	return &userv1.ListMyWarningsResponse{
		Warnings:   warningDTOs(page.Warnings),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toListWarningsForUserResponse(page WarningPage) *userv1.ListWarningsForUserResponse {
	return &userv1.ListWarningsForUserResponse{
		Warnings:   warningDTOs(page.Warnings),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toProtoPageInfo(info pagination.PageInfo) *paginationv1.PageInfo {
	return &paginationv1.PageInfo{
		Page: info.Page(), PageSize: info.PageSize(),
		TotalItems: info.TotalItems(), TotalPages: info.TotalPages(),
	}
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
