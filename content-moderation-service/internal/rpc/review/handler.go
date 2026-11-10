// Package review exposes CMS jobs; User Service owns review records.
package review

import (
	"context"
	"errors"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"

	"connectrpc.com/connect"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1"
	"github.com/google/uuid"
)

type OwnerHandler struct {
	service *moderationjob.Service
}

func NewOwnerHandler(service *moderationjob.Service) *OwnerHandler {
	return &OwnerHandler{service: service}
}

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

	jobs, err := h.service.GetJobsForReviewer(ctx, reviewerID, requestIDs)
	if err != nil {
		return nil, err
	}
	summaries := make([]*reviewv1.ReviewModerationJobSummary, 0, len(jobs))
	for _, job := range jobs {
		summaries = append(summaries, summarize(job))
	}
	resp := connect.NewResponse[reviewv1.GetReviewModerationJobsForReviewerResponse](
		&reviewv1.GetReviewModerationJobsForReviewerResponse{
			Jobs: summaries,
		},
	)
	return resp, nil
}

type AdminHandler struct {
	service *moderationjob.Service
}

func NewAdminHandler(service *moderationjob.Service) *AdminHandler {
	return &AdminHandler{service: service}
}

func (h *AdminHandler) GetReviewModerationJob(
	ctx context.Context,
	req *connect.Request[reviewv1.GetReviewModerationJobRequest],
) (*connect.Response[reviewv1.GetReviewModerationJobResponse], error) {
	jobID, err := uuid.Parse(req.Msg.JobId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("invalid job_id"))
	}
	job, err := h.service.GetJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, moderationjob.ErrJobNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errs.NewNotFoundError("job not found"))
		}
		return nil, err
	}
	attempts, err := h.service.GetAttempts(ctx, jobID, job.RunGeneration)
	if err != nil {
		return nil, err
	}
	views := make([]*reviewv1.ModerationAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		views = append(views, attemptOf(attempt))
	}
	return connect.NewResponse(&reviewv1.GetReviewModerationJobResponse{
		Job: summarize(job), Attempts: views,
	}), nil
}

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

	job, err := h.service.RetryErroredJob(
		ctx,
		jobID,
		req.Msg.ExpectedRunGeneration,
		caller.ID,
	)
	if err != nil {
		if errors.Is(err, moderationjob.ErrNotRetriable) {
			return nil, connect.NewError(
				connect.CodeFailedPrecondition,
				errs.NewFailedPreconditionError("job is not retriable at the expected generation"),
			)
		}
		if errors.Is(err, moderationjob.ErrJobNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errs.NewNotFoundError("job not found"))
		}
		return nil, err
	}
	resp := connect.NewResponse[reviewv1.RetryErroredJobResponse](
		&reviewv1.RetryErroredJobResponse{Job: summarize(job)},
	)
	return resp, nil
}
