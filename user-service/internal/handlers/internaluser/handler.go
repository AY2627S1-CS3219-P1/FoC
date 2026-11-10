// Package internaluser serves user.v1.UserInternalService, the reads Moderation
// Service needs from User Service. It is mounted only on the internal listener.
package internaluser

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

var _ userv1connect.UserInternalServiceHandler = (*Handler)(nil)

// Users lists existing users by ID.
type Users interface {
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]models.User, error)
}

// Reviews reads a review's facts.
type Reviews interface {
	ReviewFacts(ctx context.Context, id uuid.UUID) (review.Facts, error)
}

// Warnings reads a warning's facts.
type Warnings interface {
	Facts(ctx context.Context, id uuid.UUID) (warning.Facts, error)
}

// Handler adapts the internal reads to the generated Connect API.
type Handler struct {
	userv1connect.UnimplementedUserInternalServiceHandler

	Users    Users
	Reviews  Reviews
	Warnings Warnings
}

func (h *Handler) GetUsers(
	ctx context.Context,
	req *connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	ids := make([]uuid.UUID, 0, len(req.Msg.GetIds()))
	for _, raw := range req.Msg.GetIds() {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user id"))
		}
		ids = append(ids, id)
	}

	users, err := h.Users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("list users"))
	}

	out := make([]*userv1.InternalUser, 0, len(users))
	for _, user := range users {
		out = append(out, &userv1.InternalUser{
			Id:          user.ID.String(),
			DisplayName: user.DisplayName,
			Role:        string(tokenRole(user.Role)),
		})
	}
	return connect.NewResponse(&userv1.GetUsersResponse{Users: out}), nil
}

func (h *Handler) GetReviewFacts(
	ctx context.Context,
	req *connect.Request[userv1.GetReviewFactsRequest],
) (*connect.Response[userv1.GetReviewFactsResponse], error) {
	id, err := uuid.Parse(req.Msg.GetReviewId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid review id"))
	}

	facts, err := h.Reviews.ReviewFacts(ctx, id)
	if err != nil {
		return nil, toConnectError(err, "review not found")
	}

	resp := &userv1.GetReviewFactsResponse{
		ReviewerId: facts.ReviewerID.String(),
		RevieweeId: facts.RevieweeID.String(),
		OrderId:    facts.OrderID.String(),
		Removed:    facts.Removed,
	}
	if facts.RemovalReportID != uuid.Nil {
		reportID := facts.RemovalReportID.String()
		resp.RemovalReportId = &reportID
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) GetWarningFacts(
	ctx context.Context,
	req *connect.Request[userv1.GetWarningFactsRequest],
) (*connect.Response[userv1.GetWarningFactsResponse], error) {
	id, err := uuid.Parse(req.Msg.GetWarningId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid warning id"))
	}

	facts, err := h.Warnings.Facts(ctx, id)
	if errors.Is(err, warning.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("warning not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("read warning"))
	}

	resp := &userv1.GetWarningFactsResponse{
		UserId:    facts.UserID.String(),
		RequestId: facts.RequestID.String(),
		Active:    facts.Active,
	}
	if facts.ReportID != uuid.Nil {
		reportID := facts.ReportID.String()
		resp.ReportId = &reportID
	}
	return connect.NewResponse(resp), nil
}

// toConnectError keeps not-found distinct and hides other failures.
func toConnectError(err error, notFound string) error {
	var external interface{ GetConnectCode() connect.Code }
	if errors.As(err, &external) && external.GetConnectCode() == connect.CodeNotFound {
		return connect.NewError(connect.CodeNotFound, errors.New(notFound))
	}
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// tokenRole names a role the way access tokens do.
func tokenRole(role models.RoleName) tokenclaims.Role {
	switch role {
	case models.RoleSuperAdmin:
		return tokenclaims.RoleSuperAdmin
	case models.RoleAdmin:
		return tokenclaims.RoleAdmin
	case models.RoleSuspended:
		return tokenclaims.RoleSuspendedUser
	default:
		return tokenclaims.RoleUser
	}
}
