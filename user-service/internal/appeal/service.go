package appeal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/google/uuid"
)

const (
	maxReasonLength           = 4000
	maxResponseLength         = 4000
	maxResolutionReasonLength = 2000
	maxDecisions              = 10
)

// WithTransaction runs operation inside one database transaction. The
// composition root binds the appeal queries and the consequences to it.
type WithTransaction func(ctx context.Context, operation func(Tx) error) error

type Service struct {
	repo            Repository
	withTransaction WithTransaction
	evidence        Evidence
}

func NewService(repo Repository, withTransaction WithTransaction, evidence Evidence) *Service {
	return &Service{repo: repo, withTransaction: withTransaction, evidence: evidence}
}

// Submission

// SubmitAppeal records an open appeal of one or more decisions that affect
// appellantID (RP7, RP8.1). Suspended users may appeal (U6.8.1). Resubmitting
// the same key and payload returns the original appeal (RP7.7).
func (s *Service) SubmitAppeal(ctx context.Context, appellantID uuid.UUID, submission Submission) (Appeal, error) {
	if submission.IdempotencyKey == uuid.Nil {
		return Appeal{}, errs.NewBadRequestError("an idempotency key is required")
	}
	reason, err := boundedText(submission.Reason, maxReasonLength, "reason")
	if err != nil {
		return Appeal{}, err
	}
	if err := validateTargets(submission.Targets); err != nil {
		return Appeal{}, err
	}
	submission.Reason = reason
	hash := requestHash(submission)

	// A retry returns the appeal its key created before images are checked,
	// because the first submission already attached them.
	var existing ExistingAppeal
	err = s.withTransaction(ctx, func(tx Tx) error {
		var err error
		existing, err = s.repo.AppealByKey(ctx, tx.Queries, appellantID, submission.IdempotencyKey)
		return err
	})
	if err == nil {
		id, err := replay(existing, hash)
		if err != nil {
			return Appeal{}, err
		}
		return s.getAppeal(ctx, id)
	}
	if !errors.Is(err, errAppealNotFound) {
		return Appeal{}, err
	}
	prepared, err := s.evidence.Prepare(ctx, appellantID, submission.EvidenceIDs)
	if err != nil {
		return Appeal{}, err
	}

	appealID, err := newID()
	if err != nil {
		return Appeal{}, err
	}

	err = s.withTransaction(ctx, func(tx Tx) error {
		existing, err := s.repo.AppealByKey(ctx, tx.Queries, appellantID, submission.IdempotencyKey)
		if err == nil {
			appealID, err = replay(existing, hash)
			return err
		}
		if !errors.Is(err, errAppealNotFound) {
			return err
		}

		decisions, requestID, err := s.resolveTargets(ctx, tx, appellantID, submission)
		if err != nil {
			return err
		}

		inserted, err := s.repo.InsertAppeal(ctx, tx.Queries, AppealInput{
			ID:             appealID,
			AppellantID:    appellantID,
			RequestID:      requestID,
			Reason:         reason,
			IdempotencyKey: submission.IdempotencyKey,
			RequestHash:    hash,
		})
		if err != nil {
			return mapWriteError(err)
		}
		if !inserted {
			// A concurrent submission with the same key committed first.
			existing, err := s.repo.AppealByKey(ctx, tx.Queries, appellantID, submission.IdempotencyKey)
			if err != nil {
				return err
			}
			appealID, err = replay(existing, hash)
			return err
		}

		for _, decision := range decisions {
			decision.AppealID = appealID
			if err := s.repo.InsertDecision(ctx, tx.Queries, decision); err != nil {
				return mapWriteError(err)
			}
		}
		return tx.Evidence.Attach(ctx, appellantID, evidence.AppealParent(appealID), prepared)
	})
	if err != nil {
		return Appeal{}, err
	}

	return s.getAppeal(ctx, appealID)
}

// replay answers a resubmission with the appeal its key created, unless the
// payload differs.
func replay(existing ExistingAppeal, hash string) (uuid.UUID, error) {
	if existing.RequestHash != hash {
		return uuid.Nil, errs.WrapAlreadyExistsError(
			errIdempotencyKeyConflict, "this idempotency key was used for a different appeal")
	}
	return existing.ID, nil
}

func validateTargets(targets []Target) error {
	if len(targets) == 0 || len(targets) > maxDecisions {
		return errs.NewBadRequestError("an appeal must challenge between 1 and 10 decisions")
	}
	seen := make(map[Target]bool, len(targets))
	for _, target := range targets {
		if target.TargetID == uuid.Nil {
			return errs.NewBadRequestError("every appealed decision needs a target")
		}
		switch target.Kind {
		case KindReportOutcome, KindWarning, KindReviewRemoval, KindReviewRetention:
		case KindCreditAdjustment:
			return errs.NewBadRequestError("credit adjustments cannot be appealed yet")
		default:
			return errs.NewBadRequestError("invalid decision kind")
		}
		if seen[target] {
			return errs.NewBadRequestError("each decision may be appealed once per appeal")
		}
		seen[target] = true
	}
	return nil
}

// requestHash fingerprints the canonical submission. Decision order does not
// matter.
func requestHash(submission Submission) string {
	targets := make([]string, len(submission.Targets))
	for i, target := range submission.Targets {
		targets[i] = string(target.Kind) + ":" + target.TargetID.String()
	}
	slices.Sort(targets)

	sum := sha256.New()
	images := make([]string, len(submission.EvidenceIDs))
	for i, id := range submission.EvidenceIDs {
		images[i] = "evidence:" + id.String()
	}

	parts := append([]string{submission.RequestID.String(), submission.Reason}, targets...)
	for _, part := range append(parts, images...) {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// resolveTargets checks that every decision is final, affects the appellant
// and may be appealed, and settles the appeal's originating request (RP7.6).
func (s *Service) resolveTargets(
	ctx context.Context,
	tx Tx,
	appellantID uuid.UUID,
	submission Submission,
) ([]DecisionInput, uuid.UUID, error) {
	decisions := make([]DecisionInput, len(submission.Targets))
	var requests []uuid.UUID
	for i, target := range submission.Targets {
		counterpartID, requestID, err := s.resolveTarget(ctx, tx, appellantID, target)
		if err != nil {
			return nil, uuid.Nil, err
		}
		id, err := newID()
		if err != nil {
			return nil, uuid.Nil, err
		}
		decisions[i] = DecisionInput{ID: id, Target: target, CounterpartID: counterpartID}
		if requestID != uuid.Nil && !slices.Contains(requests, requestID) {
			requests = append(requests, requestID)
		}
	}

	switch {
	case submission.RequestID != uuid.Nil:
		if !slices.Contains(requests, submission.RequestID) {
			return nil, uuid.Nil, errs.NewBadRequestError("the request does not match the appealed decisions")
		}
		return decisions, submission.RequestID, nil
	case len(requests) == 1:
		return decisions, requests[0], nil
	default:
		return decisions, uuid.Nil, nil
	}
}

// resolveTarget returns the decision's counterpart and originating request.
// Decisions the caller cannot see are not found; decisions about someone else
// that the caller can see are forbidden.
func (s *Service) resolveTarget(
	ctx context.Context,
	tx Tx,
	appellantID uuid.UUID,
	target Target,
) (uuid.UUID, uuid.UUID, error) {
	switch target.Kind {
	case KindReportOutcome:
		report, err := s.repo.ReportFacts(ctx, tx.Queries, target.TargetID)
		if err != nil {
			return uuid.Nil, uuid.Nil, targetError(err, "report not found")
		}
		return reportCounterpart(appellantID, report)

	case KindWarning:
		warning, err := s.repo.WarningFacts(ctx, tx.Queries, target.TargetID)
		if err != nil {
			return uuid.Nil, uuid.Nil, targetError(err, "warning not found")
		}
		switch {
		case warning.UserID != appellantID:
			return uuid.Nil, uuid.Nil, errs.WrapNotFoundError(errTargetNotFound, "warning not found")
		case !warning.Active:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError("warning is no longer active")
		case warning.ReportID != uuid.Nil:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError(
				"appeal the outcome of the report that issued this warning")
		}
		return uuid.Nil, warning.RequestID, nil

	case KindReviewRemoval:
		review, err := s.repo.ReviewFacts(ctx, tx.Queries, target.TargetID, appellantID)
		if err != nil {
			return uuid.Nil, uuid.Nil, targetError(err, "review not found")
		}
		if err := requireAffected(appellantID, review.ReviewerID, review.RevieweeID, "review not found"); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		switch {
		case !review.Removed || !review.HasUnrestoredRemoval:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError("review is not removed")
		case review.RemovedByReport:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError(
				"appeal the outcome of the report that removed this review")
		}
		return review.RevieweeID, review.OrderID, nil

	case KindReviewRetention:
		review, err := s.repo.ReviewFacts(ctx, tx.Queries, target.TargetID, appellantID)
		if err != nil {
			return uuid.Nil, uuid.Nil, targetError(err, "review not found")
		}
		if err := requireAffected(appellantID, review.RevieweeID, review.ReviewerID, "review not found"); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		switch {
		case review.Removed:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError("review is no longer published")
		case !review.RetainedByReport:
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError(
				"only a review kept by a closed report of yours can be appealed")
		}
		return review.ReviewerID, review.OrderID, nil

	default:
		return uuid.Nil, uuid.Nil, errs.NewBadRequestError("invalid decision kind")
	}
}

// reportCounterpart lets the reported participant appeal a consequence, and
// the reporter appeal a no-action outcome on an order. A review report's
// no-action outcome is appealed as the retained review, which has an effect
// to undo.
func reportCounterpart(appellantID uuid.UUID, report ReportFacts) (uuid.UUID, uuid.UUID, error) {
	if report.Status != string(StatusClosed) {
		if report.ReporterID == appellantID || report.SubjectID == appellantID {
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError("report is not closed")
		}
		return uuid.Nil, uuid.Nil, errs.WrapNotFoundError(errTargetNotFound, "report not found")
	}

	if report.Outcome == ReportOutcomeNoAction {
		if err := requireAffected(appellantID, report.ReporterID, report.SubjectID, "report not found"); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		if report.ReviewID != uuid.Nil {
			return uuid.Nil, uuid.Nil, errs.NewFailedPreconditionError("appeal the retained review instead")
		}
		return report.SubjectID, report.RequestID, nil
	}

	if err := requireAffected(appellantID, report.SubjectID, report.ReporterID, "report not found"); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return report.ReporterID, report.RequestID, nil
}

// requireAffected admits the affected user, forbids the other participant and
// hides the decision from everyone else.
func requireAffected(appellantID, affectedID, otherID uuid.UUID, notFound string) error {
	switch appellantID {
	case affectedID:
		return nil
	case otherID:
		return errs.NewForbiddenError("only the user the decision affects can appeal it")
	default:
		return errs.WrapNotFoundError(errTargetNotFound, notFound)
	}
}

func targetError(err error, message string) error {
	if errors.Is(err, errTargetNotFound) {
		return errs.WrapNotFoundError(err, message)
	}
	return err
}

func mapWriteError(err error) error {
	switch {
	case errors.Is(err, errDecisionAlreadyAppeal):
		return errs.WrapAlreadyExistsError(err, "a decision is already under an unclosed appeal")
	case errors.Is(err, errAppealInvalid):
		return errs.WrapBadRequestError(err, "invalid appeal data")
	default:
		return err
	}
}

// Participant reads

// ListMyAppeals lists the appeals userID filed or is a counterpart of.
func (s *Service) ListMyAppeals(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (Page, error) {
	appeals, err := s.repo.ListAppealsInvolving(ctx, userID, page.Offset(), page.PageSize())
	if err != nil {
		return Page{}, err
	}
	if err := s.withEvidence(ctx, appeals, nil); err != nil {
		return Page{}, err
	}
	total, err := s.repo.CountAppealsInvolving(ctx, userID)
	if err != nil {
		return Page{}, err
	}
	return Page{Appeals: appeals, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// GetAppeal returns an appeal and its responses to an administrator, the
// appellant or a counterpart (RP11.3). Other callers see not found.
func (s *Service) GetAppeal(ctx context.Context, callerID uuid.UUID, isAdmin bool, id uuid.UUID) (Detail, error) {
	appeal, err := s.getAppeal(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if !isAdmin && !appeal.Involves(callerID) {
		return Detail{}, errs.WrapNotFoundError(errAppealNotFound, "appeal not found")
	}

	responses, err := s.repo.ListResponses(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if err := s.withEvidence(ctx, nil, responses); err != nil {
		return Detail{}, err
	}
	return Detail{Appeal: appeal, Responses: responses}, nil
}

// Responses

// SubmitResponse records the reply of the participant an administrator asked
// to respond, with any images (RP9.3, RP9.4), and moves the appeal back under
// review.
func (s *Service) SubmitResponse(
	ctx context.Context,
	callerID, appealID uuid.UUID,
	body string,
	evidenceIDs []uuid.UUID,
) (Appeal, Response, error) {
	body, err := boundedText(body, maxResponseLength, "response")
	if err != nil {
		return Appeal{}, Response{}, err
	}
	prepared, err := s.evidence.Prepare(ctx, callerID, evidenceIDs)
	if err != nil {
		return Appeal{}, Response{}, err
	}
	responseID, err := newID()
	if err != nil {
		return Appeal{}, Response{}, err
	}

	err = s.withTransaction(ctx, func(tx Tx) error {
		locked, decisions, err := s.lock(ctx, tx, appealID)
		if err != nil {
			return err
		}
		if locked.AppellantID != callerID && !hasCounterpart(decisions, callerID) {
			return errs.WrapNotFoundError(errAppealNotFound, "appeal not found")
		}
		if locked.Status.IsClosed() {
			return errs.NewFailedPreconditionError("appeal is closed")
		}
		if locked.Status != StatusAwaitingResponse || locked.AwaitedUserID != callerID {
			return errs.NewForbiddenError("no response has been requested from you")
		}

		input := ResponseInput{ID: responseID, AppealID: appealID, ResponderID: callerID, Body: body}
		if err := s.repo.InsertResponse(ctx, tx.Queries, input); err != nil {
			return err
		}
		if err := tx.Evidence.Attach(ctx, callerID, evidence.AppealResponseParent(responseID), prepared); err != nil {
			return err
		}
		return s.repo.TransitionAppeal(
			ctx, tx.Queries, appealID, StatusAwaitingResponse, StatusUnderReview, uuid.Nil)
	})
	if err != nil {
		return Appeal{}, Response{}, err
	}

	detail, err := s.GetAppeal(ctx, callerID, false, appealID)
	if err != nil {
		return Appeal{}, Response{}, err
	}
	for _, response := range detail.Responses {
		if response.ID == responseID {
			return detail.Appeal, response, nil
		}
	}
	return Appeal{}, Response{}, errors.New("submitted response not found after commit")
}

// Administration

func (s *Service) ListAppeals(ctx context.Context, filter Filter, page pagination.Pagination) (Page, error) {
	appeals, err := s.repo.ListAppeals(ctx, filter, page.Offset(), page.PageSize())
	if err != nil {
		return Page{}, err
	}
	if err := s.withEvidence(ctx, appeals, nil); err != nil {
		return Page{}, err
	}
	total, err := s.repo.CountAppeals(ctx, filter)
	if err != nil {
		return Page{}, err
	}
	return Page{Appeals: appeals, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// BeginReview moves an open appeal under review (RP10).
func (s *Service) BeginReview(ctx context.Context, actorID, id uuid.UUID) (Appeal, error) {
	err := s.withTransaction(ctx, func(tx Tx) error {
		locked, _, err := s.lockForAdmin(ctx, tx, actorID, id)
		if err != nil {
			return err
		}
		if err := requireStatus(locked, StatusOpen); err != nil {
			return err
		}
		return s.repo.TransitionAppeal(ctx, tx.Queries, id, StatusOpen, StatusUnderReview, uuid.Nil)
	})
	if err != nil {
		return Appeal{}, err
	}
	return s.getAppeal(ctx, id)
}

// RequestResponse asks a counterpart of the appealed decisions to respond
// (RP9.1, RP9.2).
func (s *Service) RequestResponse(ctx context.Context, actorID, id, participantID uuid.UUID) (Appeal, error) {
	err := s.withTransaction(ctx, func(tx Tx) error {
		locked, decisions, err := s.lockForAdmin(ctx, tx, actorID, id)
		if err != nil {
			return err
		}
		if err := requireStatus(locked, StatusUnderReview); err != nil {
			return err
		}
		if !hasCounterpart(decisions, participantID) {
			return errs.NewBadRequestError("the participant is not affected by the appealed decisions")
		}
		return s.repo.TransitionAppeal(
			ctx, tx.Queries, id, StatusUnderReview, StatusAwaitingResponse, participantID)
	})
	if err != nil {
		return Appeal{}, err
	}
	return s.getAppeal(ctx, id)
}

// CloseAppeal closes an unclosed appeal with one outcome per decision (RP11)
// and undoes every overturned decision in the same transaction (RP12).
func (s *Service) CloseAppeal(ctx context.Context, actorID, id uuid.UUID, resolutions []Resolution) (Appeal, error) {
	resolved := make(map[uuid.UUID]Resolution, len(resolutions))
	for _, resolution := range resolutions {
		if !resolution.Outcome.IsValid() {
			return Appeal{}, errs.NewBadRequestError("invalid decision outcome")
		}
		reason, err := boundedText(resolution.Reason, maxResolutionReasonLength, "outcome reason")
		if err != nil {
			return Appeal{}, err
		}
		if _, duplicate := resolved[resolution.DecisionID]; duplicate {
			return Appeal{}, errs.NewBadRequestError("resolve each appealed decision exactly once")
		}
		resolution.Reason = reason
		resolved[resolution.DecisionID] = resolution
	}

	err := s.withTransaction(ctx, func(tx Tx) error {
		locked, decisions, err := s.lockForAdmin(ctx, tx, actorID, id)
		if err != nil {
			return err
		}
		if locked.Status.IsClosed() {
			return errs.NewFailedPreconditionError("appeal is already closed")
		}
		if len(decisions) != len(resolved) {
			return errs.NewBadRequestError("resolve each appealed decision exactly once")
		}
		for _, decision := range decisions {
			if _, ok := resolved[decision.ID]; !ok {
				return errs.NewBadRequestError("resolve each appealed decision exactly once")
			}
		}

		if err := s.repo.CloseAppeal(ctx, tx.Queries, id, actorID); err != nil {
			return err
		}
		for _, decision := range decisions {
			resolution := resolved[decision.ID]
			if err := s.repo.ResolveDecision(ctx, tx.Queries, id, resolution); err != nil {
				return err
			}
			if resolution.Outcome != OutcomeOverturned {
				continue
			}
			if err := overturn(ctx, tx.Consequences, id, actorID, decision, resolution.Reason); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Appeal{}, err
	}
	return s.getAppeal(ctx, id)
}

// overturn undoes one decision (RP12.1-RP12.5). An overturned no-action
// report outcome records the disposition only.
func overturn(
	ctx context.Context,
	consequences Consequences,
	appealID, actorID uuid.UUID,
	decision LockedDecision,
	reason string,
) error {
	target := decision.Target.TargetID
	switch decision.Target.Kind {
	case KindWarning:
		return consequences.RemoveWarning(ctx, WarningRemoval{
			WarningID: target, AppealID: appealID, ActorID: actorID, Reason: reason,
		})
	case KindReviewRemoval:
		return consequences.RestoreReview(ctx, ReviewRestoration{
			ReviewID: target, AppealID: appealID, ActorID: actorID, Reason: reason,
		})
	case KindReviewRetention:
		return consequences.RemoveReview(ctx, actorID, target, reason)
	case KindReportOutcome:
		return overturnReport(ctx, consequences, appealID, actorID, decision, reason)
	default:
		return errs.NewFailedPreconditionError("this decision cannot be overturned yet")
	}
}

func overturnReport(
	ctx context.Context,
	consequences Consequences,
	appealID, actorID uuid.UUID,
	decision LockedDecision,
	reason string,
) error {
	switch decision.ReportOutcome {
	case ReportOutcomeWarning:
		if decision.ReportWarningID == uuid.Nil {
			return errs.NewFailedPreconditionError("the report's warning no longer exists")
		}
		return consequences.RemoveWarning(ctx, WarningRemoval{
			WarningID: decision.ReportWarningID, AppealID: appealID, ActorID: actorID, Reason: reason,
		})
	case ReportOutcomeSuspension:
		return consequences.Reinstate(ctx, Reinstatement{
			UserID:   decision.ReportSubjectID,
			ReportID: decision.Target.TargetID,
			ActorID:  actorID,
			Reason:   reason,
		})
	case ReportOutcomeReviewRemoval:
		return consequences.RestoreReview(ctx, ReviewRestoration{
			ReviewID: decision.ReportReviewID, AppealID: appealID, ActorID: actorID, Reason: reason,
		})
	default:
		return nil
	}
}

// Helpers

func (s *Service) lock(ctx context.Context, tx Tx, id uuid.UUID) (LockedAppeal, []LockedDecision, error) {
	locked, err := s.repo.LockAppeal(ctx, tx.Queries, id)
	if errors.Is(err, errAppealNotFound) {
		return LockedAppeal{}, nil, errs.WrapNotFoundError(err, "appeal not found")
	}
	if err != nil {
		return LockedAppeal{}, nil, err
	}
	decisions, err := s.repo.LockedDecisions(ctx, tx.Queries, id)
	if err != nil {
		return LockedAppeal{}, nil, err
	}
	return locked, decisions, nil
}

// lockForAdmin locks the appeal and rejects an administrator who filed it or
// is affected by one of its decisions.
func (s *Service) lockForAdmin(
	ctx context.Context,
	tx Tx,
	actorID, id uuid.UUID,
) (LockedAppeal, []LockedDecision, error) {
	locked, decisions, err := s.lock(ctx, tx, id)
	if err != nil {
		return LockedAppeal{}, nil, err
	}
	if locked.AppellantID == actorID || hasCounterpart(decisions, actorID) {
		return LockedAppeal{}, nil, errs.NewForbiddenError("cannot act on an appeal you filed or one that affects you")
	}
	return locked, decisions, nil
}

func requireStatus(locked LockedAppeal, want Status) error {
	if locked.Status != want {
		return errs.NewFailedPreconditionError("appeal is " + strings.ReplaceAll(string(locked.Status), "_", " "))
	}
	return nil
}

func hasCounterpart(decisions []LockedDecision, userID uuid.UUID) bool {
	for _, decision := range decisions {
		if decision.CounterpartID != uuid.Nil && decision.CounterpartID == userID {
			return true
		}
	}
	return false
}

func (s *Service) getAppeal(ctx context.Context, id uuid.UUID) (Appeal, error) {
	appeal, err := s.repo.GetAppeal(ctx, id)
	if errors.Is(err, errAppealNotFound) {
		return Appeal{}, errs.WrapNotFoundError(err, "appeal not found")
	}
	if err != nil {
		return Appeal{}, err
	}

	appeals := []Appeal{appeal}
	if err := s.withEvidence(ctx, appeals, nil); err != nil {
		return Appeal{}, err
	}
	return appeals[0], nil
}

// boundedText trims value and requires between 1 and limit characters.
func boundedText(value string, limit int, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > limit {
		return "", errs.NewBadRequestError(field + " must not be blank or too long")
	}
	return value, nil
}
