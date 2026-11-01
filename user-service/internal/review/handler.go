package review

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Logic interface {
	SubmitReview(ctx context.Context, reviewerID, revieweeID, orderID uuid.UUID, submission ReviewSubmission) error
	ListReviewsForUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error
	ListReviewRemovals(ctx context.Context, page pagination.Pagination) (RemovalPage, error)
}

// Handler adapts the review proto RPCs to the review service.
type Handler struct {
	reviewv1connect.UnimplementedReviewServiceHandler
	Logic Logic
}

// SubmitReview records the caller's review of the other participant; `from`
// must be the caller so impersonation is rejected.
func (h *Handler) SubmitReview(
	ctx context.Context,
	req *connect.Request[reviewv1.SubmitReviewRequest],
) (*connect.Response[reviewv1.SubmitReviewResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	from, err := uuid.Parse(req.Msg.GetFrom())
	if err != nil || from == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid reviewer ID"))
	}
	if from != actorID {
		return nil, api.ToConnectError(ctx, errs.NewForbiddenError("reviewer must be the authenticated caller"))
	}
	to, err := uuid.Parse(req.Msg.GetTo())
	if err != nil || to == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid reviewee ID"))
	}
	orderID, err := uuid.Parse(req.Msg.GetOrderId())
	if err != nil || orderID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid order ID"))
	}
	if err := h.Logic.SubmitReview(
		ctx,
		actorID,
		to,
		orderID,
		reviewv1SubmissionToDomain(req.Msg.GetSubmission()),
	); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.SubmitReviewResponse{}), nil
}

// ListReviewsForUser lists the reviews a user received; a suspended caller may
// only list their own.
func (h *Handler) ListReviewsForUser(
	ctx context.Context,
	req *connect.Request[reviewv1.ListReviewsForUserRequest],
) (*connect.Response[reviewv1.ListReviewsForUserResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	userID, err := uuid.Parse(req.Msg.GetUserId())
	if err != nil || userID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	if err := authorizeSelfReviews(caller, userID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewsForUser(ctx, userID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(toListReviewsForUserResponse(pageResult)), nil
}

func (h *Handler) ListMyReviews(
	ctx context.Context,
	req *connect.Request[reviewv1.ListMyReviewsRequest],
) (*connect.Response[reviewv1.ListMyReviewsResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewsFromUser(ctx, actorID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(toListMyReviewsResponse(pageResult)), nil
}

// RemoveReview soft-deletes a review and records the removal audit row; the
// admin-only gate lives in the interceptor.
func (h *Handler) RemoveReview(
	ctx context.Context,
	req *connect.Request[reviewv1.RemoveReviewRequest],
) (*connect.Response[reviewv1.RemoveReviewResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil || id == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid review ID"))
	}
	if err := h.Logic.RemoveReview(ctx, id, req.Msg.GetReason(), actorID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.RemoveReviewResponse{}), nil
}

func (h *Handler) ListReviewRemovals(
	ctx context.Context,
	req *connect.Request[reviewv1.ListReviewRemovalsRequest],
) (*connect.Response[reviewv1.ListReviewRemovalsResponse], error) {
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewRemovals(ctx, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(toListReviewRemovalsResponse(pageResult)), nil
}

// authorizeSelfReviews is the ABAC rule that is too tied to the request message
// for the token-claim interceptor: a suspended caller may only list the reviews
// received by their own user.
func authorizeSelfReviews(c userdto.Caller, userID uuid.UUID) error {
	if !c.IsSuspended() {
		return nil
	}
	callerID, err := uuid.Parse(c.ID)
	if err != nil || callerID != userID {
		return errs.NewForbiddenError("suspended users may only list their own reviews")
	}
	return nil
}

// newPage normalizes the optional proto pagination into the shared model.
func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(int(p.GetPage()), int(p.GetPageSize()))
}

func reviewv1SubmissionToDomain(submission *reviewv1.ReviewSubmission) ReviewSubmission {
	return ReviewSubmission{Rating: int(submission.GetRating()), Comment: submission.GetComment()}
}

func toListReviewsForUserResponse(page ReviewPage) *reviewv1.ListReviewsForUserResponse {
	return &reviewv1.ListReviewsForUserResponse{
		Reviews:    toReviewProtos(page.Reviews),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toListMyReviewsResponse(page ReviewPage) *reviewv1.ListMyReviewsResponse {
	return &reviewv1.ListMyReviewsResponse{
		Reviews:    toReviewProtos(page.Reviews),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toListReviewRemovalsResponse(page RemovalPage) *reviewv1.ListReviewRemovalsResponse {
	out := make([]*reviewv1.ReviewRemoval, len(page.Removals))
	for i, removal := range page.Removals {
		out[i] = toReviewRemovalProto(removal)
	}
	return &reviewv1.ListReviewRemovalsResponse{
		Removals:   out,
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

func toReviewProtos(reviews []Review) []*reviewv1.Review {
	out := make([]*reviewv1.Review, len(reviews))
	for i, review := range reviews {
		out[i] = toReviewProto(review)
	}
	return out
}

func toProtoPageInfo(info pagination.PageInfo) *paginationv1.PageInfo {
	return &paginationv1.PageInfo{
		Page:       int32(info.Page()),
		PageSize:   int32(info.PageSize()),
		TotalItems: info.TotalItems(),
		TotalPages: int32(info.TotalPages()),
	}
}

func toReviewProto(review Review) *reviewv1.Review {
	return &reviewv1.Review{
		Id:           review.ID.String(),
		ReviewerId:   review.ReviewerID.String(),
		ReviewerName: review.ReviewerName,
		RevieweeId:   review.RevieweeID.String(),
		RevieweeName: review.RevieweeName,
		OrderId:      review.OrderID.String(),
		Rating:       int32(review.Rating),
		Comment:      review.Comment,
		CreatedAt:    timestamppb.New(review.CreatedAt),
	}
}

func toReviewRemovalProto(removal ReviewRemoval) *reviewv1.ReviewRemoval {
	return &reviewv1.ReviewRemoval{
		Id:            removal.ID.String(),
		ReviewerId:    removal.ReviewerID.String(),
		RevieweeId:    removal.RevieweeID.String(),
		OrderId:       removal.OrderID.String(),
		Reason:        removal.Reason,
		CreatedAt:     timestamppb.New(removal.CreatedAt),
		CreatedBy:     removal.CreatedBy.String(),
		CreatedByName: removal.CreatedByName,
	}
}
