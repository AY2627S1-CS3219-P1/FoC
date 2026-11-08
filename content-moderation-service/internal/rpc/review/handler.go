// Package review maps the moderation.review.v1 contract to the review domain:
// the owner-visible job summaries and the admin operations on errored jobs.
// Authorization is owner-or-admin; ordinary callers may only query their own
// reviewer_id.
package review

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	review "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1"
	"github.com/google/uuid"
)

// OwnerHandler implements the owner-facing ReviewModerationService.
type OwnerHandler struct {
	store *review.Store
}

// NewOwnerHandler builds the owner service from the job store.
func NewOwnerHandler(store *review.Store) *OwnerHandler {
	return &OwnerHandler{store: store}
}

// GetReviewModerationJobsForReviewer returns the caller's jobs for the requested
// moderation request IDs. Non-admin callers may only supply their own reviewer_id.
func (h *OwnerHandler) GetReviewModerationJobsForReviewer(
	ctx context.Context,
	req *connect.Request[reviewv1.GetReviewModerationJobsForReviewerRequest],
) (*connect.Response[reviewv1.GetReviewModerationJobsForReviewerResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errs.NewUnauthorizedError("unauthenticated"))
	}

	reviewerID, err := uuid.Parse(req.Msg.ReviewerId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid reviewer_id"))
	}
	if !caller.Admin && caller.ID != reviewerID.String() {
		return nil, connect.NewError(connect.CodePermissionDenied, errs.NewForbiddenError("permission denied"))
	}

	requestIDs := make([]uuid.UUID, 0, len(req.Msg.ModerationRequestIds))
	for _, id := range req.Msg.ModerationRequestIds {
		parsed, perr := uuid.Parse(id)
		if perr != nil {
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				errs.NewBadRequestError("invalid moderation_request_id"),
			)
		}
		requestIDs = append(requestIDs, parsed)
	}

	jobs, err := h.store.GetJobsForReviewer(ctx, reviewerID, requestIDs)
	if err != nil {
		return nil, err
	}
	resp := connect.NewResponse[reviewv1.GetReviewModerationJobsForReviewerResponse](
		&reviewv1.GetReviewModerationJobsForReviewerResponse{
			Jobs: jobs,
		},
	)
	return resp, nil
}

// AdminHandler implements the admin-facing ReviewModerationAdminService.
type AdminHandler struct {
	store *review.Store
}

// NewAdminHandler builds the admin service from the job store.
func NewAdminHandler(store *review.Store) *AdminHandler {
	return &AdminHandler{store: store}
}

// GetReviewModerationJob returns bounded operational detail for one job.
func (h *AdminHandler) GetReviewModerationJob(
	ctx context.Context,
	req *connect.Request[reviewv1.GetReviewModerationJobRequest],
) (*connect.Response[reviewv1.GetReviewModerationJobResponse], error) {
	jobID, err := uuid.Parse(req.Msg.JobId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid job_id"))
	}
	resp, err := h.store.GetModerationJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, review.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errs.NewNotFoundError("job not found"))
		}
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// RetryErroredJob requeues a terminal errored job once for the expected
// generation.
func (h *AdminHandler) RetryErroredJob(
	ctx context.Context,
	req *connect.Request[reviewv1.RetryErroredJobRequest],
) (*connect.Response[reviewv1.RetryErroredJobResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errs.NewUnauthorizedError("unauthenticated"))
	}
	jobID, err := uuid.Parse(req.Msg.JobId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid job_id"))
	}

	job, err := h.store.RetryErroredJob(
		ctx,
		jobID,
		req.Msg.ExpectedRunGeneration,
		req.Msg.UseCurrentConfiguration,
		caller.ID,
	)
	if err != nil {
		if errors.Is(err, review.ErrNotRetriable) {
			return nil, connect.NewError(
				connect.CodeFailedPrecondition,
				errs.NewFailedPreconditionError("job is not retriable at the expected generation"),
			)
		}
		if errors.Is(err, review.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errs.NewNotFoundError("job not found"))
		}
		return nil, err
	}
	resp := connect.NewResponse[reviewv1.RetryErroredJobResponse](&reviewv1.RetryErroredJobResponse{Job: job})
	return resp, nil
}
