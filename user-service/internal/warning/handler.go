package warning

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	warningv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/warning/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/warning/v1/warningv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WarningLogic is the read operation the user-facing WarningService adapter calls.
type WarningLogic interface {
	ListWarnings(context.Context, uuid.UUID, pagination.Pagination) (WarningPage, error)
}

// Handler is the user-facing WarningService adapter.
type Handler struct {
	warningv1connect.UnimplementedWarningServiceHandler
	Logic WarningLogic
}

// ListMyWarnings returns the authenticated caller's own warnings.
func (h *Handler) ListMyWarnings(
	ctx context.Context,
	req *connect.Request[warningv1.ListMyWarningsRequest],
) (*connect.Response[warningv1.ListMyWarningsResponse], error) {
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

// AdminWarningLogic is the operations the admin-only WarningAdminService
// adapter calls; the service is administrator-gated at the mount
// (auth.RequireAdmin()) rather than per-method.
type AdminWarningLogic interface {
	ListWarningsForAdmin(context.Context, uuid.UUID, pagination.Pagination) (AdminWarningPage, error)
	MarkWarningAsRemoved(context.Context, uuid.UUID, string, *uuid.UUID, uuid.UUID) error
}

// AdminHandler is the admin-only WarningAdminService adapter.
type AdminHandler struct {
	warningv1connect.UnimplementedWarningAdminServiceHandler
	Logic AdminWarningLogic
}

// ListWarningsForUser returns the admin read view of a user's warnings.
func (h *AdminHandler) ListWarningsForUser(
	ctx context.Context,
	req *connect.Request[warningv1.ListWarningsForUserRequest],
) (*connect.Response[warningv1.ListWarningsForUserResponse], error) {
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
	warnings, err := h.Logic.ListWarningsForAdmin(ctx, userID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(toListWarningsForUserResponse(warnings))
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

// MarkWarningAsRemoved records an audit row for a warning.
func (h *AdminHandler) MarkWarningAsRemoved(
	ctx context.Context,
	req *connect.Request[warningv1.MarkWarningAsRemovedRequest],
) (*connect.Response[warningv1.MarkWarningAsRemovedResponse], error) {
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
	if err := h.Logic.MarkWarningAsRemoved(ctx, warningID, req.Msg.GetRemovedReason(), appealID, actorID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&warningv1.MarkWarningAsRemovedResponse{}), nil
}

func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(p.GetPage(), p.GetPageSize())
}

func toListMyWarningsResponse(page WarningPage) *warningv1.ListMyWarningsResponse {
	return &warningv1.ListMyWarningsResponse{
		Warnings:   warningDTOs(page.Warnings),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toListWarningsForUserResponse(page AdminWarningPage) *warningv1.ListWarningsForUserResponse {
	return &warningv1.ListWarningsForUserResponse{
		Warnings:   adminWarningDTOs(page.Warnings),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toProtoPageInfo(info pagination.PageInfo) *paginationv1.PageInfo {
	return &paginationv1.PageInfo{
		Page: info.Page(), PageSize: info.PageSize(),
		TotalItems: info.TotalItems(), TotalPages: info.TotalPages(),
	}
}

func warningDTOs(warnings []Warning) []*warningv1.AccountWarning {
	result := make([]*warningv1.AccountWarning, len(warnings))
	for i, warning := range warnings {
		item := &warningv1.AccountWarning{
			Id: warning.ID.String(), RequestId: warning.RequestID.String(), Reason: warning.Reason,
			Status: warningStatus(warning.Status), CreatedAt: timestamppb.New(warning.CreatedAt),
		}
		item.RemovedAt, item.RemovedReason = removalFields(warning)
		result[i] = item
	}
	return result
}

func adminWarningDTOs(warnings []AdminWarning) []*warningv1.AdminAccountWarning {
	result := make([]*warningv1.AdminAccountWarning, len(warnings))
	for i, admin := range warnings {
		item := &warningv1.AdminAccountWarning{
			Id: admin.ID.String(), RequestId: admin.RequestID.String(), Reason: admin.Reason,
			Status: warningStatus(admin.Status), CreatedAt: timestamppb.New(admin.CreatedAt),
			UpdatedAt: timestamppb.New(admin.UpdatedAt),
		}
		item.RemovedAt, item.RemovedReason = removalFields(admin.Warning)
		if admin.SourceEventID != nil {
			item.SourceEventId = stringPointer(admin.SourceEventID.String())
		}
		if admin.UpdatedBy != nil {
			item.UpdatedBy = stringPointer(admin.UpdatedBy.String())
		}
		if admin.UpdatedByName != nil {
			item.UpdatedByName = admin.UpdatedByName
		}
		result[i] = item
	}
	return result
}

func removalFields(warning Warning) (*timestamppb.Timestamp, *string) {
	var removedAt *timestamppb.Timestamp
	if warning.RemovedAt != nil {
		removedAt = timestamppb.New(*warning.RemovedAt)
	}
	return removedAt, warning.RemovedReason
}

func warningStatus(status Status) warningv1.WarningStatus {
	switch status {
	case StatusActive:
		return warningv1.WarningStatus_WARNING_STATUS_ACTIVE
	case StatusRemoved:
		return warningv1.WarningStatus_WARNING_STATUS_REMOVED
	default:
		return warningv1.WarningStatus_WARNING_STATUS_UNSPECIFIED
	}
}
