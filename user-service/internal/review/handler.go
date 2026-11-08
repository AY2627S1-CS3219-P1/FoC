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
	SubmitReview(
		ctx context.Context,
		reviewerID, revieweeID, orderID uuid.UUID,
		submission ReviewSubmission,
	) (SubmissionResult, error)
	ListReviewsForUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListUnpublishedReviewsForUser(
		ctx context.Context, reviewerID uuid.UUID, filter UnpublishedFilter, page pagination.Pagination,
	) (UnpublishedReviewPage, error)
	RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error
	ListReviewRemovals(ctx context.Context, page pagination.Pagination) (RemovalPage, error)
}

// Handler adapts the review proto RPCs to the review service.
type Handler struct {
	reviewv1connect.UnimplementedReviewServiceHandler
	Logic Logic
}

// SubmitReview records the caller's pending review of the other participant;
// `from` must be the caller so impersonation is rejected.
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
	result, err := h.Logic.SubmitReview(
		ctx,
		actorID,
		to,
		orderID,
		reviewv1SubmissionToDomain(req.Msg.GetSubmission()),
	)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.SubmitReviewResponse{
		ReviewId:            result.ReviewID.String(),
		ModerationRequestId: result.ModerationRequestID.String(),
		ModerationStatus:    toProtoModerationStatus(result.Status),
	}), nil
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

// ListUnpublishedReviewsForUser lists the reviewer's pending, rejected and
// moderation-error reviews. Authorization runs before any lookup: ordinary and
// suspended callers may only inspect their own reviews, admins any reviewer.
func (h *Handler) ListUnpublishedReviewsForUser(
	ctx context.Context,
	req *connect.Request[reviewv1.ListUnpublishedReviewsForUserRequest],
) (*connect.Response[reviewv1.ListUnpublishedReviewsForUserResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	reviewerID, err := uuid.Parse(req.Msg.GetReviewerId())
	if err != nil || reviewerID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid reviewer ID"))
	}
	if err := authorizeUnpublishedReviews(caller, reviewerID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	filter, err := unpublishedFilter(req.Msg)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListUnpublishedReviewsForUser(ctx, reviewerID, filter, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(toListUnpublishedReviewsResponse(pageResult)), nil
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
	return requireOwnIdentity(c, userID, "suspended users may only list their own reviews")
}

// authorizeUnpublishedReviews is the ABAC rule for the unpublished list:
// administrators may select any reviewer; everyone else, including suspended
// callers, may only inspect their own.
func authorizeUnpublishedReviews(c userdto.Caller, reviewerID uuid.UUID) error {
	if c.Admin && !c.IsSuspended() {
		return nil
	}
	return requireOwnIdentity(c, reviewerID, "callers may only list their own unpublished reviews")
}

func requireOwnIdentity(c userdto.Caller, userID uuid.UUID, message string) error {
	callerID, err := uuid.Parse(c.ID)
	if err != nil || callerID != userID {
		return errs.NewForbiddenError(message)
	}
	return nil
}

// unpublishedFilter maps the optional proto filters. Only the three
// unpublished statuses are valid; asking the collection for approved reviews is
// a client error.
func unpublishedFilter(req *reviewv1.ListUnpublishedReviewsForUserRequest) (UnpublishedFilter, error) {
	filter := UnpublishedFilter{}
	if p := req.ModerationStatusFilter; p != nil {
		switch *p {
		case reviewv1.ModerationStatus_MODERATION_STATUS_PENDING_MODERATION:
			pending := ModerationPending
			filter.Status = &pending
		case reviewv1.ModerationStatus_MODERATION_STATUS_REJECTED:
			rejected := ModerationRejected
			filter.Status = &rejected
		case reviewv1.ModerationStatus_MODERATION_STATUS_MODERATION_ERROR:
			errored := ModerationError
			filter.Status = &errored
		default:
			return UnpublishedFilter{}, errs.NewBadRequestError(
				"moderation status filter must be pending moderation, rejected or moderation error")
		}
	}
	if orderID, err := uuid.Parse(req.GetOrderId()); err == nil && orderID != uuid.Nil {
		filter.OrderID = &orderID
	} else if req.GetOrderId() != "" {
		return UnpublishedFilter{}, errs.NewBadRequestError("invalid order ID")
	}
	return filter, nil
}

// newPage normalizes the optional proto pagination into the shared model.
func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(p.GetPage(), p.GetPageSize())
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

func toListUnpublishedReviewsResponse(page UnpublishedReviewPage) *reviewv1.ListUnpublishedReviewsForUserResponse {
	out := make([]*reviewv1.UnpublishedReview, len(page.Reviews))
	for i, review := range page.Reviews {
		out[i] = toUnpublishedReviewProto(review)
	}
	return &reviewv1.ListUnpublishedReviewsForUserResponse{
		Reviews:    out,
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
		Page:       info.Page(),
		PageSize:   info.PageSize(),
		TotalItems: info.TotalItems(),
		TotalPages: info.TotalPages(),
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

func toUnpublishedReviewProto(review UnpublishedReview) *reviewv1.UnpublishedReview {
	out := &reviewv1.UnpublishedReview{
		Id:                    review.ID.String(),
		ReviewerId:            review.ReviewerID.String(),
		ReviewerName:          review.ReviewerName,
		RevieweeId:            review.RevieweeID.String(),
		RevieweeName:          review.RevieweeName,
		OrderId:               review.OrderID.String(),
		Rating:                int32(review.Rating),
		Comment:               review.Comment,
		CreatedAt:             timestamppb.New(review.CreatedAt),
		ModerationStatus:      toProtoModerationStatus(review.ModerationStatus),
		ModerationRequestId:   review.ModerationRequestID.String(),
		ContentRevision:       int32(review.ContentRevision),
		ModerationReasonCodes: review.ModerationReasonCodes,
		AppliedRunGeneration:  review.AppliedRunGeneration,
	}
	if !review.ModeratedAt.IsZero() {
		out.ModeratedAt = timestamppb.New(review.ModeratedAt)
	}
	return out
}

func toProtoModerationStatus(status ModerationStatus) reviewv1.ModerationStatus {
	switch status {
	case ModerationPending:
		return reviewv1.ModerationStatus_MODERATION_STATUS_PENDING_MODERATION
	case ModerationApproved:
		return reviewv1.ModerationStatus_MODERATION_STATUS_APPROVED
	case ModerationRejected:
		return reviewv1.ModerationStatus_MODERATION_STATUS_REJECTED
	case ModerationError:
		return reviewv1.ModerationStatus_MODERATION_STATUS_MODERATION_ERROR
	default:
		return reviewv1.ModerationStatus_MODERATION_STATUS_UNSPECIFIED
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
