package report

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/orders"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/users"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

const (
	maxDescriptionLength = 4000
	maxResponseLength    = 4000
	maxReasonLength      = 2000
)

// WithTransaction runs operation inside one database transaction. The
// composition root binds the report queries and the consequences to it.
type WithTransaction func(ctx context.Context, operation func(Tx) error) error

type Service struct {
	repo            Repository
	withTransaction WithTransaction
	evidence        Evidence
	orders          orders.Gateway
	directory       Directory
}

func NewService(
	repo Repository,
	withTransaction WithTransaction,
	evidence Evidence,
	orders orders.Gateway,
	directory Directory,
) *Service {
	return &Service{
		repo:            repo,
		withTransaction: withTransaction,
		evidence:        evidence,
		orders:          orders,
		directory:       directory,
	}
}

// Submission

// SubmitReport records an open report by reporterID (RP1, RP3.1). A review
// target's subject is the review's author; an order target names its subject.
func (s *Service) SubmitReport(ctx context.Context, reporterID uuid.UUID, submission Submission) (Report, error) {
	if !submission.Target.IsValid() {
		return Report{}, errs.NewBadRequestError("a report must target exactly one review or one order")
	}
	if !submission.Category.IsValid() {
		return Report{}, errs.NewBadRequestError("invalid report category")
	}
	description, err := boundedText(submission.Description, maxDescriptionLength, "description")
	if err != nil {
		return Report{}, err
	}

	subjectID, requestID, err := s.resolveSubject(ctx, reporterID, submission.Target)
	if err != nil {
		return Report{}, err
	}
	prepared, err := s.evidence.Prepare(ctx, reporterID, submission.EvidenceIDs)
	if err != nil {
		return Report{}, err
	}

	id, err := newID()
	if err != nil {
		return Report{}, err
	}
	input := ReportInput{
		ID:          id,
		ReporterID:  reporterID,
		SubjectID:   subjectID,
		ReviewID:    submission.Target.ReviewID,
		OrderID:     submission.Target.OrderID,
		RequestID:   requestID,
		Category:    submission.Category,
		Description: description,
	}
	err = s.withTransaction(ctx, func(tx Tx) error {
		if err := s.repo.InsertReport(ctx, tx.Queries, input); err != nil {
			switch {
			case errors.Is(err, errReportAlreadyOpen):
				return errs.WrapAlreadyExistsError(err, "you already have an unclosed report for this target")
			case errors.Is(err, errReportInvalid):
				return errs.WrapBadRequestError(err, "invalid report data")
			default:
				return err
			}
		}
		return tx.Evidence.Attach(ctx, reporterID, evidence.ReportParent(id), prepared)
	})
	if err != nil {
		return Report{}, err
	}

	return s.getReport(ctx, id)
}

// resolveSubject finds the reported user and the originating order, and
// rejects self-reports. A review target's subject is its author; an order
// target must name the order's other participant.
func (s *Service) resolveSubject(
	ctx context.Context,
	reporterID uuid.UUID,
	target Target,
) (subjectID, requestID uuid.UUID, err error) {
	if target.IsReview() {
		review, err := s.directory.ReviewFacts(ctx, target.ReviewID)
		if err != nil {
			return uuid.Nil, uuid.Nil, users.ExternalError(err, "review not found")
		}
		// A removed review cannot be newly reported.
		if review.Removed {
			return uuid.Nil, uuid.Nil, errs.WrapNotFoundError(errReviewNotFound, "review not found")
		}
		if review.ReviewerID == reporterID {
			return uuid.Nil, uuid.Nil, errs.NewBadRequestError("cannot report your own review")
		}
		return review.ReviewerID, review.OrderID, nil
	}

	if target.SubjectID == reporterID {
		return uuid.Nil, uuid.Nil, errs.NewBadRequestError("cannot report yourself")
	}
	people, err := s.directory.People(ctx, []uuid.UUID{target.SubjectID})
	if err != nil {
		return uuid.Nil, uuid.Nil, users.ExternalError(err, "reported user not found")
	}
	if _, ok := people[target.SubjectID]; !ok {
		return uuid.Nil, uuid.Nil, errs.NewNotFoundError("reported user not found")
	}

	// Only the order's requester and courier may report each other (RP1.1,
	// RP1.2). The lookup fails closed when Order cannot answer.
	order, err := s.orders.GetOrderParticipants(ctx, target.OrderID)
	if err != nil {
		return uuid.Nil, uuid.Nil, orders.ExternalError(err)
	}
	if !order.Contains(reporterID, target.SubjectID) {
		return uuid.Nil, uuid.Nil, errs.NewForbiddenError(
			"only the order's requester and courier can report each other",
		)
	}
	return target.SubjectID, target.OrderID, nil
}

// Participant reads

// ListMyReports lists the reports userID filed or is the subject of (RP4).
func (s *Service) ListMyReports(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (Page, error) {
	reports, err := s.repo.ListReportsInvolving(ctx, userID, page.Offset(), page.PageSize())
	if err != nil {
		return Page{}, err
	}
	s.withNames(ctx, reports, nil)
	if err := s.withEvidence(ctx, reports, nil); err != nil {
		return Page{}, err
	}
	total, err := s.repo.CountReportsInvolving(ctx, userID)
	if err != nil {
		return Page{}, err
	}
	return Page{Reports: reports, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// GetReport returns a report and its responses to an administrator, the
// reporter or the reported participant. Other callers see not found, so
// report IDs reveal nothing.
func (s *Service) GetReport(ctx context.Context, callerID uuid.UUID, isAdmin bool, id uuid.UUID) (Detail, error) {
	report, err := s.getReport(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if !isAdmin && !report.Involves(callerID) {
		return Detail{}, errs.WrapNotFoundError(errReportNotFound, "report not found")
	}

	responses, err := s.repo.ListResponses(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	s.withNames(ctx, nil, responses)
	if err := s.withEvidence(ctx, nil, responses); err != nil {
		return Detail{}, err
	}
	return Detail{Report: report, Responses: responses}, nil
}

// Responses

// SubmitResponse records the reported participant's reply with any images
// (RP2). A reply to a requested response moves the report back under review
// (RP3.4).
func (s *Service) SubmitResponse(
	ctx context.Context,
	callerID, reportID uuid.UUID,
	body string,
	evidenceIDs []uuid.UUID,
) (Report, Response, error) {
	body, err := boundedText(body, maxResponseLength, "response")
	if err != nil {
		return Report{}, Response{}, err
	}
	prepared, err := s.evidence.Prepare(ctx, callerID, evidenceIDs)
	if err != nil {
		return Report{}, Response{}, err
	}
	responseID, err := newID()
	if err != nil {
		return Report{}, Response{}, err
	}

	err = s.withTransaction(ctx, func(tx Tx) error {
		locked, err := s.lock(ctx, tx, reportID)
		if err != nil {
			return err
		}
		switch {
		case locked.SubjectID == callerID:
		case locked.ReporterID == callerID:
			return errs.NewForbiddenError("only the reported participant can respond")
		default:
			return errs.WrapNotFoundError(errReportNotFound, "report not found")
		}
		if locked.Status.IsClosed() {
			return errs.NewFailedPreconditionError("report is closed")
		}

		input := ResponseInput{ID: responseID, ReportID: reportID, ResponderID: callerID, Body: body}
		if err := s.repo.InsertResponse(ctx, tx.Queries, input); err != nil {
			return err
		}
		if err := tx.Evidence.Attach(ctx, callerID, evidence.ReportResponseParent(responseID), prepared); err != nil {
			return err
		}
		if locked.Status == StatusAwaitingResponse {
			return s.repo.TransitionReport(ctx, tx.Queries, reportID, StatusAwaitingResponse, StatusUnderReview)
		}
		return nil
	})
	if err != nil {
		return Report{}, Response{}, err
	}

	detail, err := s.GetReport(ctx, callerID, false, reportID)
	if err != nil {
		return Report{}, Response{}, err
	}
	for _, response := range detail.Responses {
		if response.ID == responseID {
			return detail.Report, response, nil
		}
	}
	return Report{}, Response{}, errors.New("submitted response not found after commit")
}

// Administration

func (s *Service) ListReports(ctx context.Context, filter Filter, page pagination.Pagination) (Page, error) {
	reports, err := s.repo.ListReports(ctx, filter, page.Offset(), page.PageSize())
	if err != nil {
		return Page{}, err
	}
	s.withNames(ctx, reports, nil)
	if err := s.withEvidence(ctx, reports, nil); err != nil {
		return Page{}, err
	}
	total, err := s.repo.CountReports(ctx, filter)
	if err != nil {
		return Page{}, err
	}
	return Page{Reports: reports, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// BeginReview moves an open report under review (RP3.2).
func (s *Service) BeginReview(ctx context.Context, actorID, id uuid.UUID) (Report, error) {
	return s.transition(ctx, actorID, id, StatusOpen, StatusUnderReview)
}

// RequestResponse asks the reported participant to respond (RP3.3).
func (s *Service) RequestResponse(ctx context.Context, actorID, id uuid.UUID) (Report, error) {
	return s.transition(ctx, actorID, id, StatusUnderReview, StatusAwaitingResponse)
}

// CloseReport closes an unclosed report with a reasoned outcome (RP3.5) and
// records that outcome for User Service in the same transaction (RP5).
func (s *Service) CloseReport(
	ctx context.Context,
	actorID, id uuid.UUID,
	outcome Outcome,
	reason string,
) (Report, error) {
	if !outcome.IsValid() {
		return Report{}, errs.NewBadRequestError("invalid report outcome")
	}
	reason, err := boundedText(reason, maxReasonLength, "outcome reason")
	if err != nil {
		return Report{}, err
	}

	err = s.withTransaction(ctx, func(tx Tx) error {
		locked, err := s.lockForAdmin(ctx, tx, actorID, id)
		if err != nil {
			return err
		}
		if locked.Status.IsClosed() {
			return errs.NewFailedPreconditionError("report is already closed")
		}
		if outcome == OutcomeReviewRemoval && locked.ReviewID == uuid.Nil {
			return errs.NewFailedPreconditionError("review removal requires a report about a review")
		}
		if outcome == OutcomeSuspension {
			if err := s.checkSuspendable(ctx, locked.SubjectID); err != nil {
				return err
			}
		}

		closure := Closure{Outcome: outcome, Reason: reason, ClosedBy: actorID}
		if err := s.repo.CloseReport(ctx, tx.Queries, id, closure); err != nil {
			return err
		}
		return applyOutcome(ctx, tx.Consequences, id, actorID, locked, outcome, reason)
	})
	if err != nil {
		return Report{}, err
	}

	return s.getReport(ctx, id)
}

// applyOutcome records the consequence of a closed report against the reported
// participant (RP5.1-RP5.4). User Service applies it from the outbox.
func applyOutcome(
	ctx context.Context,
	consequences Consequences,
	reportID, actorID uuid.UUID,
	locked LockedReport,
	outcome Outcome,
	reason string,
) error {
	switch outcome {
	case OutcomeWarning:
		return consequences.RecordWarning(ctx, Warning{
			UserID:    locked.SubjectID,
			RequestID: locked.RequestID,
			ReportID:  reportID,
			ActorID:   actorID,
			Reason:    reason,
		})
	case OutcomeSuspension:
		return consequences.Suspend(ctx, Suspension{
			UserID:   locked.SubjectID,
			ReportID: reportID,
			ActorID:  actorID,
			Reason:   reason,
		})
	case OutcomeReviewRemoval:
		return consequences.RemoveReview(ctx, ReviewRemoval{
			ReviewID: locked.ReviewID,
			ReportID: reportID,
			ActorID:  actorID,
			Reason:   reason,
		})
	default:
		return nil
	}
}

// Helpers

// transition moves a report between two unclosed statuses on behalf of an
// administrator.
func (s *Service) transition(ctx context.Context, actorID, id uuid.UUID, from, to Status) (Report, error) {
	err := s.withTransaction(ctx, func(tx Tx) error {
		locked, err := s.lockForAdmin(ctx, tx, actorID, id)
		if err != nil {
			return err
		}
		if locked.Status != from {
			return errs.NewFailedPreconditionError("report is " + strings.ReplaceAll(string(locked.Status), "_", " "))
		}
		return s.repo.TransitionReport(ctx, tx.Queries, id, from, to)
	})
	if err != nil {
		return Report{}, err
	}

	return s.getReport(ctx, id)
}

func (s *Service) lock(ctx context.Context, tx Tx, id uuid.UUID) (LockedReport, error) {
	locked, err := s.repo.LockReport(ctx, tx.Queries, id)
	if errors.Is(err, errReportNotFound) {
		return LockedReport{}, errs.WrapNotFoundError(err, "report not found")
	}
	return locked, err
}

// lockForAdmin locks the report and rejects an administrator who filed it or
// is its subject.
func (s *Service) lockForAdmin(ctx context.Context, tx Tx, actorID, id uuid.UUID) (LockedReport, error) {
	locked, err := s.lock(ctx, tx, id)
	if err != nil {
		return LockedReport{}, err
	}
	if locked.Involves(actorID) {
		return LockedReport{}, errs.NewForbiddenError("cannot act on a report you filed or one about you")
	}
	return locked, nil
}

func (s *Service) getReport(ctx context.Context, id uuid.UUID) (Report, error) {
	report, err := s.repo.GetReport(ctx, id)
	if errors.Is(err, errReportNotFound) {
		return Report{}, errs.WrapNotFoundError(err, "report not found")
	}
	if err != nil {
		return Report{}, err
	}

	reports := []Report{report}
	s.withNames(ctx, reports, nil)
	if err := s.withEvidence(ctx, reports, nil); err != nil {
		return Report{}, err
	}
	return reports[0], nil
}

// checkSuspendable refuses a suspension User Service would reject, so the
// administrator learns now rather than after the report closes.
func (s *Service) checkSuspendable(ctx context.Context, subjectID uuid.UUID) error {
	people, err := s.directory.People(ctx, []uuid.UUID{subjectID})
	if err != nil {
		return users.ExternalError(err, "reported user not found")
	}
	subject, ok := people[subjectID]
	switch {
	case !ok:
		return errs.NewFailedPreconditionError("the reported user no longer exists")
	case subject.IsAdmin():
		return errs.NewFailedPreconditionError("an administrator cannot be suspended through a report")
	case subject.IsSuspended():
		return errs.NewFailedPreconditionError("the reported user is already suspended")
	}
	return nil
}

// withNames fills display names from User Service. Names are only shown, so
// they stay empty when User Service cannot answer.
func (s *Service) withNames(ctx context.Context, reports []Report, responses []Response) {
	var ids []uuid.UUID
	for _, report := range reports {
		ids = append(ids, report.ReporterID, report.SubjectID, report.ClosedBy)
	}
	for _, response := range responses {
		ids = append(ids, response.ResponderID)
	}
	if len(ids) == 0 {
		return
	}

	names := users.Names(ctx, s.directory, ids)
	for i := range reports {
		reports[i].ReporterName = names[reports[i].ReporterID]
		reports[i].SubjectName = names[reports[i].SubjectID]
		reports[i].CloserName = names[reports[i].ClosedBy]
	}
	for i := range responses {
		responses[i].ResponderName = names[responses[i].ResponderID]
	}
}

// boundedText trims value and requires between 1 and limit characters.
func boundedText(value string, limit int, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > limit {
		return "", errs.NewBadRequestError(field + " must not be blank or too long")
	}
	return value, nil
}
