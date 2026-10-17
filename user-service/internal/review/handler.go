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
	ListReviewFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error
	ListReviewRemovals(ctx context.Context) ([]ReviewRemoval, error)
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
func (h *Handler) ListReviewsForUser(ctx context.Context, req *connect.Request[reviewv1.ListReviewsForUserRequest]) (*connect.Response[reviewv1.ReviewPage], error) {
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
	return connect.NewResponse(toReviewPage(pageResult)), nil
}

// ListMyReviews lists the reviews the caller gave.
func (h *Handler) ListMyReviews(ctx context.Context, req *connect.Request[reviewv1.ListMyReviewsRequest]) (*connect.Response[reviewv1.ReviewPage], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewFromUser(ctx, actorID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(toReviewPage(pageResult)), nil
}

// RemoveReview hard-deletes a review on behalf of an administrator and records
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

// ListReviewRemovals returns audit rows for removed reviews, newest first.
func (h *Handler) ListReviewRemovals(ctx context.Context, _ *connect.Request[reviewv1.ListReviewRemovalsRequest]) (*connect.Response[reviewv1.ReviewRemovalsResponse], error) {
	removals, err := h.Logic.ListReviewRemovals(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	out := make([]*reviewv1.ReviewRemoval, len(removals))
	for i, removal := range removals {
		out[i] = toReviewRemovalProto(removal)
	}
	return connect.NewResponse(&reviewv1.ReviewRemovalsResponse{Removals: out}), nil
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

func toReviewPage(page ReviewPage) *reviewv1.ReviewPage {
	reviews := make([]*reviewv1.Review, len(page.Reviews))
	for i, review := range page.Reviews {
		reviews[i] = toReviewProto(review)
	}
	return &reviewv1.ReviewPage{
		Reviews: reviews,
		Pagination: &paginationv1.PageInfo{
			Page:       int32(page.PageInfo.Page()),
			PageSize:   int32(page.PageInfo.PageSize()),
			TotalItems: page.PageInfo.TotalItems(),
			TotalPages: int32(page.PageInfo.TotalPages()),
		},
	}
}

func toReviewProto(review Review) *reviewv1.Review {
	return &reviewv1.Review{
		Id:         review.ID.String(),
		ReviewerId: review.ReviewerID.String(),
		RevieweeId: review.RevieweeID.String(),
		OrderId:    review.OrderID.String(),
		Rating:     int32(review.Rating),
		Comment:    review.Comment,
		CreatedAt:  timestamppb.New(review.CreatedAt),
	}
}

func toReviewRemovalProto(removal ReviewRemoval) *reviewv1.ReviewRemoval {
	return &reviewv1.ReviewRemoval{
		Id:         removal.ID.String(),
		ReviewerId: removal.ReviewerID.String(),
		RevieweeId: removal.RevieweeID.String(),
		OrderId:    removal.OrderID.String(),
		Reason:     removal.Reason,
		CreatedAt:  timestamppb.New(removal.CreatedAt),
		CreatedBy:  removal.CreatedBy.String(),
	}
}
