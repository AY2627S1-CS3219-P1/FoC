package review

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
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
		photoIDs []uuid.UUID,
	) error
	CreateReviewPhotoUpload(
		ctx context.Context,
		callerID uuid.UUID,
		contentType string,
		sizeBytes int64,
	) (uuid.UUID, images.UploadGrant, error)
	ListReviewsForUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error)
	ListMyReviewsForOrders(ctx context.Context, reviewerID uuid.UUID, orderIDs []uuid.UUID) ([]uuid.UUID, error)
	GetMyReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error)
	RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error
	ListReviewRemovals(ctx context.Context, page pagination.Pagination) (RemovalPage, error)
}

// Handler adapts the review proto RPCs to the review service.
type Handler struct {
	reviewv1connect.UnimplementedReviewServiceHandler
	Logic Logic
}

// SubmitReview records the caller's review of the other participant.
func (h *Handler) SubmitReview(
	ctx context.Context,
	req *connect.Request[reviewv1.SubmitReviewRequest],
) (*connect.Response[reviewv1.SubmitReviewResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	from, err := uuid.Parse(req.Msg.GetFrom())
	if err != nil || from == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid reviewer ID"))
	}
	if from != actorID {
		return nil, toConnectError(ctx, errs.NewForbiddenError("reviewer must be the authenticated caller"))
	}
	to, err := uuid.Parse(req.Msg.GetTo())
	if err != nil || to == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid reviewee ID"))
	}
	orderID, err := uuid.Parse(req.Msg.GetOrderId())
	if err != nil || orderID == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid order ID"))
	}
	photoIDs, err := reviewv1PhotoIDsToDomain(req.Msg.GetPhotoIds())
	if err != nil {
		return nil, toConnectError(ctx, errs.WrapBadRequestError(err, "invalid photo ID"))
	}
	if err := h.Logic.SubmitReview(
		ctx,
		actorID,
		to,
		orderID,
		reviewv1SubmissionToDomain(req.Msg.GetSubmission()),
		photoIDs,
	); err != nil {
		return nil, toConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.SubmitReviewResponse{}), nil
}

// CreateReviewPhotoUpload grants one pre-signed PUT; bytes go straight to storage.
func (h *Handler) CreateReviewPhotoUpload(
	ctx context.Context,
	req *connect.Request[reviewv1.CreateReviewPhotoUploadRequest],
) (*connect.Response[reviewv1.CreateReviewPhotoUploadResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	photoID, grant, err := h.Logic.CreateReviewPhotoUpload(
		ctx, actorID, req.Msg.GetContentType(), req.Msg.GetSizeBytes(),
	)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	resp := connect.NewResponse(&reviewv1.CreateReviewPhotoUploadResponse{
		PhotoId:       photoID.String(),
		UploadUrl:     grant.URL,
		UploadHeaders: grant.Headers,
		ExpiresAt:     timestamppb.New(grant.ExpiresAt),
	})
	noStore(resp)
	return resp, nil
}

// ListReviewsForUser lists received reviews; a suspended caller may only list their own.
func (h *Handler) ListReviewsForUser(
	ctx context.Context,
	req *connect.Request[reviewv1.ListReviewsForUserRequest],
) (*connect.Response[reviewv1.ListReviewsForUserResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	userID, err := uuid.Parse(req.Msg.GetUserId())
	if err != nil || userID == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	if err := authorizeSelfReviews(caller, userID); err != nil {
		return nil, toConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, toConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewsForUser(ctx, userID, page)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	resp := connect.NewResponse(toListReviewsForUserResponse(pageResult))
	noStore(resp)
	return resp, nil
}

func (h *Handler) ListMyReviews(
	ctx context.Context,
	req *connect.Request[reviewv1.ListMyReviewsRequest],
) (*connect.Response[reviewv1.ListMyReviewsResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, toConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewsFromUser(ctx, actorID, page)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	resp := connect.NewResponse(toListMyReviewsResponse(pageResult))
	noStore(resp)
	return resp, nil
}

// noStore marks responses embedding short-lived pre-signed URLs as non-cacheable.
func noStore[R any](resp *connect.Response[R]) {
	resp.Header().Set("Cache-Control", "no-store")
}

// ListMyReviewsForOrders returns which of the requests the caller has reviewed.
func (h *Handler) ListMyReviewsForOrders(
	ctx context.Context,
	req *connect.Request[reviewv1.ListMyReviewsForOrdersRequest],
) (*connect.Response[reviewv1.ListMyReviewsForOrdersResponse], error) {
	callerID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	orderIDs := make([]uuid.UUID, 0, len(req.Msg.GetOrderIds()))
	for _, raw := range req.Msg.GetOrderIds() {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil || id == uuid.Nil {
			return nil, toConnectError(ctx, errs.NewBadRequestError("invalid order ID"))
		}
		orderIDs = append(orderIDs, id)
	}
	reviewed, err := h.Logic.ListMyReviewsForOrders(ctx, callerID, orderIDs)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	ids := make([]string, 0, len(reviewed))
	for _, id := range reviewed {
		ids = append(ids, id.String())
	}
	return connect.NewResponse(&reviewv1.ListMyReviewsForOrdersResponse{ReviewedOrderIds: ids}), nil
}

// GetMyReviewForOrder returns the caller's review, or NotFound when unreviewed; not cacheable.
func (h *Handler) GetMyReviewForOrder(
	ctx context.Context,
	req *connect.Request[reviewv1.GetMyReviewForOrderRequest],
) (*connect.Response[reviewv1.GetMyReviewForOrderResponse], error) {
	callerID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	orderID, parseErr := uuid.Parse(req.Msg.GetOrderId())
	if parseErr != nil || orderID == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid order ID"))
	}
	review, err := h.Logic.GetMyReviewForOrder(ctx, callerID, orderID)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	resp := connect.NewResponse(&reviewv1.GetMyReviewForOrderResponse{Review: toReviewProto(review)})
	noStore(resp)
	return resp, nil
}

// RemoveReview soft-deletes a review and records the audit row.
func (h *Handler) RemoveReview(
	ctx context.Context,
	req *connect.Request[reviewv1.RemoveReviewRequest],
) (*connect.Response[reviewv1.RemoveReviewResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil || id == uuid.Nil {
		return nil, toConnectError(ctx, errs.NewBadRequestError("invalid review ID"))
	}
	if err := h.Logic.RemoveReview(ctx, id, req.Msg.GetReason(), actorID); err != nil {
		return nil, toConnectError(ctx, err)
	}
	return connect.NewResponse(&reviewv1.RemoveReviewResponse{}), nil
}

func (h *Handler) ListReviewRemovals(
	ctx context.Context,
	req *connect.Request[reviewv1.ListReviewRemovalsRequest],
) (*connect.Response[reviewv1.ListReviewRemovalsResponse], error) {
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, toConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}
	pageResult, err := h.Logic.ListReviewRemovals(ctx, page)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	return connect.NewResponse(toListReviewRemovalsResponse(pageResult)), nil
}

// A suspended caller may only list reviews received by their own user.
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
	return pagination.New(p.GetPage(), p.GetPageSize())
}

func reviewv1SubmissionToDomain(submission *reviewv1.ReviewSubmission) ReviewSubmission {
	return ReviewSubmission{Rating: int(submission.GetRating()), Comment: submission.GetComment()}
}

// reviewv1PhotoIDsToDomain parses the validated photo IDs. Every entry must be
// a well-formed, non-zero UUID: a silent nil on a malformed entry would drop
// the whole attachment list and submit a review without its photos.
func reviewv1PhotoIDsToDomain(ids []string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, errs.NewBadRequestError("photo IDs must be non-zero UUIDs")
		}
		out = append(out, id)
	}
	return out, nil
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
		Photos:       toReviewPhotoProtos(review.Photos),
	}
}

func toReviewPhotoProtos(photos []ReviewPhotoView) []*reviewv1.ReviewPhoto {
	if len(photos) == 0 {
		return nil
	}
	out := make([]*reviewv1.ReviewPhoto, len(photos))
	for i, photo := range photos {
		out[i] = toReviewPhotoProto(photo)
	}
	return out
}

func toReviewPhotoProto(photo ReviewPhotoView) *reviewv1.ReviewPhoto {
	return &reviewv1.ReviewPhoto{
		Id:          photo.ID.String(),
		Url:         photo.URL,
		ContentType: photo.ContentType,
		ExpiresAt:   timestamppb.New(photo.ExpiresAt),
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
