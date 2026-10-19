package review

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Logic is the review surface the handler needs from the service. It lists only
// the methods this adapter calls; the concrete service implements it.
type Logic interface {
	SubmitReview(ctx context.Context, reviewerID, revieweeID, orderID uuid.UUID, submission ReviewSubmission) error
	ListReviewsForUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error
	ListReviewRemovals(ctx context.Context, page pagination.Pagination) (RemovalPage, error)
}

// Handler adapts the review proto RPCs to the review service. It maps between
// protobuf and domain types and maps domain errors to Connect codes; business
// rules stay in the service.
type Handler struct {
	reviewv1connect.UnimplementedReviewServiceHandler
	Logic Logic
}

// SubmitReview records one participant's review of the other. The reviewer is
// the authenticated caller; the request's `from` must match it so impersonation
// is rejected.
func (h *Handler) SubmitReview(ctx context.Context, req *connect.Request[reviewv1.SubmitReviewRequest]) (*connect.Response[reviewv1.SubmitReviewResponse], error) {
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
	if err := h.Logic.SubmitReview(ctx, actorID, to, orderID, reviewv1SubmissionToDomain(req.Msg.GetSubmission())); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.SubmitReviewResponse{}), nil
}

// ListReviewsForUser lists the reviews received by a user. A suspended caller is
// restricted to their own reviews by authorizeSelfReviews.
func (h *Handler) ListReviewsForUser(ctx context.Context, req *connect.Request[reviewv1.ListReviewsForUserRequest]) (*connect.Response[reviewv1.ListReviewsForUserResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
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

// ListMyReviews lists the reviews the caller gave.
func (h *Handler) ListMyReviews(ctx context.Context, req *connect.Request[reviewv1.ListMyReviewsRequest]) (*connect.Response[reviewv1.ListMyReviewsResponse], error) {
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

// RemoveReview soft-deletes a review on behalf of an administrator and records
// the audit row. RBAC (admin only) is enforced by the interceptor.
func (h *Handler) RemoveReview(ctx context.Context, req *connect.Request[reviewv1.RemoveReviewRequest]) (*connect.Response[reviewv1.RemoveReviewResponse], error) {
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

// ListReviewRemovals returns a page of audit rows for removed reviews, newest
// first.
func (h *Handler) ListReviewRemovals(ctx context.Context, req *connect.Request[reviewv1.ListReviewRemovalsRequest]) (*connect.Response[reviewv1.ListReviewRemovalsResponse], error) {
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
func authorizeSelfReviews(c auth.Caller, userID uuid.UUID) error {
	if c.Role != string(tokenclaims.RoleSuspendedUser) {
		return nil
	}
	callerID, err := uuid.Parse(c.ID)
	if err != nil || callerID != userID {
		return errs.NewForbiddenError("suspended users may only list their own reviews")
	}
	return nil
}

// newPage normalizes the optional proto pagination into the shared model; a nil
// or empty page means page 1 at the default size.
func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(int(p.GetPage()), int(p.GetPageSize()))
}

func reviewv1SubmissionToDomain(submission *reviewv1.ReviewSubmission) ReviewSubmission {
	return ReviewSubmission{Rating: int(submission.GetRating()), Comment: submission.GetComment()}
}

// toListReviewsForUserResponse builds the response for the list-received RPC.
func toListReviewsForUserResponse(page ReviewPage) *reviewv1.ListReviewsForUserResponse {
	return &reviewv1.ListReviewsForUserResponse{
		Reviews:    toReviewProtos(page.Reviews),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

// toListMyReviewsResponse builds the response for the list-given RPC.
func toListMyReviewsResponse(page ReviewPage) *reviewv1.ListMyReviewsResponse {
	return &reviewv1.ListMyReviewsResponse{
		Reviews:    toReviewProtos(page.Reviews),
		Pagination: toProtoPageInfo(page.PageInfo),
	}
}

// toListReviewRemovalsResponse builds the response for the removals-audit RPC.
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

// toReviewProtos converts a slice of reviews to the proto slice.
func toReviewProtos(reviews []Review) []*reviewv1.Review {
	out := make([]*reviewv1.Review, len(reviews))
	for i, review := range reviews {
		out[i] = toReviewProto(review)
	}
	return out
}

// toProtoPageInfo maps the shared pagination model to the proto PageInfo.
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
