package appeal

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/google/uuid"
)

// fakeRepository keeps appeals in memory and enforces the idempotency key and
// active-decision uniqueness the database provides.
type fakeRepository struct {
	reports  map[uuid.UUID]ReportFacts
	warnings map[uuid.UUID]WarningFacts
	reviews  map[uuid.UUID]ReviewFacts

	appeals   map[uuid.UUID]*Appeal
	keys      map[[2]uuid.UUID]ExistingAppeal
	locked    map[uuid.UUID][]LockedDecision
	responses []Response
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		reports:  map[uuid.UUID]ReportFacts{},
		warnings: map[uuid.UUID]WarningFacts{},
		reviews:  map[uuid.UUID]ReviewFacts{},
		appeals:  map[uuid.UUID]*Appeal{},
		keys:     map[[2]uuid.UUID]ExistingAppeal{},
		locked:   map[uuid.UUID][]LockedDecision{},
	}
}

func (f *fakeRepository) AppealByKey(
	_ context.Context,
	_ *appealdb.Queries,
	appellantID, key uuid.UUID,
) (ExistingAppeal, error) {
	existing, ok := f.keys[[2]uuid.UUID{appellantID, key}]
	if !ok {
		return ExistingAppeal{}, errAppealNotFound
	}
	return existing, nil
}

func (f *fakeRepository) InsertAppeal(_ context.Context, _ *appealdb.Queries, input AppealInput) (bool, error) {
	key := [2]uuid.UUID{input.AppellantID, input.IdempotencyKey}
	if _, ok := f.keys[key]; ok {
		return false, nil
	}
	f.keys[key] = ExistingAppeal{ID: input.ID, RequestHash: input.RequestHash}
	f.appeals[input.ID] = &Appeal{
		ID:          input.ID,
		AppellantID: input.AppellantID,
		RequestID:   input.RequestID,
		Reason:      input.Reason,
		Status:      StatusOpen,
	}
	return true, nil
}

func (f *fakeRepository) InsertDecision(_ context.Context, _ *appealdb.Queries, input DecisionInput) error {
	for _, appeal := range f.appeals {
		for _, decision := range appeal.Decisions {
			if decision.Target == input.Target && decision.Outcome == "" {
				return errDecisionAlreadyAppeal
			}
		}
	}
	appeal := f.appeals[input.AppealID]
	appeal.Decisions = append(appeal.Decisions, Decision{
		ID:            input.ID,
		AppealID:      input.AppealID,
		Target:        input.Target,
		CounterpartID: input.CounterpartID,
	})
	locked := LockedDecision{ID: input.ID, Target: input.Target, CounterpartID: input.CounterpartID}
	if report, ok := f.reports[input.Target.TargetID]; ok && input.Target.Kind == KindReportOutcome {
		locked.ReportOutcome = report.Outcome
		locked.ReportSubjectID = report.SubjectID
		locked.ReportReviewID = report.ReviewID
	}
	f.locked[input.AppealID] = append(f.locked[input.AppealID], locked)
	return nil
}

func (f *fakeRepository) ReportFacts(_ context.Context, _ *appealdb.Queries, id uuid.UUID) (ReportFacts, error) {
	report, ok := f.reports[id]
	if !ok {
		return ReportFacts{}, errTargetNotFound
	}
	return report, nil
}

func (f *fakeRepository) WarningFacts(_ context.Context, _ *appealdb.Queries, id uuid.UUID) (WarningFacts, error) {
	warning, ok := f.warnings[id]
	if !ok {
		return WarningFacts{}, errTargetNotFound
	}
	return warning, nil
}

func (f *fakeRepository) ReviewFacts(_ context.Context, _ *appealdb.Queries, id, _ uuid.UUID) (ReviewFacts, error) {
	review, ok := f.reviews[id]
	if !ok {
		return ReviewFacts{}, errTargetNotFound
	}
	return review, nil
}

func (f *fakeRepository) GetAppeal(_ context.Context, id uuid.UUID) (Appeal, error) {
	appeal, ok := f.appeals[id]
	if !ok {
		return Appeal{}, errAppealNotFound
	}
	return *appeal, nil
}

func (f *fakeRepository) ListResponses(_ context.Context, appealID uuid.UUID) ([]Response, error) {
	var out []Response
	for _, response := range f.responses {
		if response.AppealID == appealID {
			out = append(out, response)
		}
	}
	return out, nil
}

func (f *fakeRepository) ListAppealsInvolving(_ context.Context, userID uuid.UUID, _, _ int64) ([]Appeal, error) {
	var out []Appeal
	for _, appeal := range f.appeals {
		if appeal.Involves(userID) {
			out = append(out, *appeal)
		}
	}
	return out, nil
}

func (f *fakeRepository) CountAppealsInvolving(ctx context.Context, userID uuid.UUID) (int64, error) {
	appeals, _ := f.ListAppealsInvolving(ctx, userID, 0, 0)
	return int64(len(appeals)), nil
}

func (f *fakeRepository) ListAppeals(_ context.Context, filter Filter, _, _ int64) ([]Appeal, error) {
	var out []Appeal
	for _, appeal := range f.appeals {
		if filter.Status == "" || appeal.Status == filter.Status {
			out = append(out, *appeal)
		}
	}
	return out, nil
}

func (f *fakeRepository) CountAppeals(ctx context.Context, filter Filter) (int64, error) {
	appeals, _ := f.ListAppeals(ctx, filter, 0, 0)
	return int64(len(appeals)), nil
}

func (f *fakeRepository) LockAppeal(_ context.Context, _ *appealdb.Queries, id uuid.UUID) (LockedAppeal, error) {
	appeal, ok := f.appeals[id]
	if !ok {
		return LockedAppeal{}, errAppealNotFound
	}
	return LockedAppeal{
		AppellantID:   appeal.AppellantID,
		Status:        appeal.Status,
		AwaitedUserID: appeal.AwaitedUserID,
	}, nil
}

func (f *fakeRepository) LockedDecisions(
	_ context.Context,
	_ *appealdb.Queries,
	appealID uuid.UUID,
) ([]LockedDecision, error) {
	return f.locked[appealID], nil
}

func (f *fakeRepository) TransitionAppeal(
	_ context.Context,
	_ *appealdb.Queries,
	id uuid.UUID,
	from, to Status,
	awaited uuid.UUID,
) error {
	appeal := f.appeals[id]
	if appeal.Status != from {
		return errAppealStateChanged
	}
	appeal.Status = to
	appeal.AwaitedUserID = awaited
	return nil
}

func (f *fakeRepository) CloseAppeal(_ context.Context, _ *appealdb.Queries, id, closedBy uuid.UUID) error {
	appeal := f.appeals[id]
	if appeal.Status.IsClosed() {
		return errAppealStateChanged
	}
	appeal.Status = StatusClosed
	appeal.AwaitedUserID = uuid.Nil
	appeal.ClosedBy = closedBy
	return nil
}

func (f *fakeRepository) ResolveDecision(
	_ context.Context,
	_ *appealdb.Queries,
	appealID uuid.UUID,
	resolution Resolution,
) error {
	appeal := f.appeals[appealID]
	for i := range appeal.Decisions {
		if appeal.Decisions[i].ID == resolution.DecisionID {
			appeal.Decisions[i].Outcome = resolution.Outcome
			appeal.Decisions[i].OutcomeReason = resolution.Reason
			return nil
		}
	}
	return errAppealStateChanged
}

func (f *fakeRepository) InsertResponse(_ context.Context, _ *appealdb.Queries, input ResponseInput) error {
	f.responses = append(f.responses, Response{
		ID:          input.ID,
		AppealID:    input.AppealID,
		ResponderID: input.ResponderID,
		Body:        input.Body,
	})
	return nil
}

// fakeConsequences records what closing asked the owning domains to undo.
type fakeConsequences struct {
	removedWarnings []WarningRemoval
	restored        []ReviewRestoration
	removedReviews  []uuid.UUID
	reinstated      []Reinstatement
	err             error
}

func (f *fakeConsequences) RemoveWarning(_ context.Context, removal WarningRemoval) error {
	f.removedWarnings = append(f.removedWarnings, removal)
	return f.err
}

func (f *fakeConsequences) RestoreReview(_ context.Context, restoration ReviewRestoration) error {
	f.restored = append(f.restored, restoration)
	return f.err
}

func (f *fakeConsequences) RemoveReview(_ context.Context, _, reviewID uuid.UUID, _ string) error {
	f.removedReviews = append(f.removedReviews, reviewID)
	return f.err
}

func (f *fakeConsequences) Reinstate(_ context.Context, reinstatement Reinstatement) error {
	f.reinstated = append(f.reinstated, reinstatement)
	return f.err
}

// fakeEvidence prepares the images it staged and records what was attached.
type fakeEvidence struct {
	staged   map[uuid.UUID]bool
	attached map[evidence.Parent][]uuid.UUID
}

func newFakeEvidence() *fakeEvidence {
	return &fakeEvidence{staged: map[uuid.UUID]bool{}, attached: map[evidence.Parent][]uuid.UUID{}}
}

func (f *fakeEvidence) CreateUpload(context.Context, uuid.UUID, string, int64) (uuid.UUID, images.UploadGrant, error) {
	id := uuid.New()
	f.staged[id] = true
	return id, images.UploadGrant{}, nil
}

func (f *fakeEvidence) Prepare(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]evidence.Prepared, error) {
	prepared := make([]evidence.Prepared, 0, len(ids))
	for _, id := range ids {
		if !f.staged[id] {
			return nil, errs.NewFailedPreconditionError("image is not pending upload")
		}
		prepared = append(prepared, evidence.Prepared{ID: id})
	}
	return prepared, nil
}

func (f *fakeEvidence) Views(
	_ context.Context,
	parents []evidence.Parent,
) (map[evidence.Parent][]evidence.View, error) {
	views := map[evidence.Parent][]evidence.View{}
	for _, parent := range parents {
		for _, id := range f.attached[parent] {
			views[parent] = append(views[parent], evidence.View{ID: id})
		}
	}
	return views, nil
}

func (f *fakeEvidence) Locate(_ context.Context, id uuid.UUID) (evidence.Owner, error) {
	for parent, ids := range f.attached {
		if parent.Kind == evidence.ParentAppeal && slices.Contains(ids, id) {
			return evidence.Owner{AppealID: parent.ID}, nil
		}
	}
	return evidence.Owner{}, errs.NewNotFoundError("evidence not found")
}

func (f *fakeEvidence) Download(_ context.Context, id uuid.UUID) (evidence.View, error) {
	return evidence.View{ID: id}, nil
}

// Attach marks images attached, so they can no longer be prepared.
func (f *fakeEvidence) Attach(
	_ context.Context,
	_ uuid.UUID,
	parent evidence.Parent,
	prepared []evidence.Prepared,
) error {
	for _, image := range prepared {
		f.attached[parent] = append(f.attached[parent], image.ID)
		delete(f.staged, image.ID)
	}
	return nil
}

func newTestService(repo *fakeRepository, consequences *fakeConsequences) *Service {
	return newTestServiceWithEvidence(repo, consequences, newFakeEvidence())
}

func newTestServiceWithEvidence(repo *fakeRepository, consequences *fakeConsequences, images *fakeEvidence) *Service {
	return NewService(repo, func(_ context.Context, operation func(Tx) error) error {
		return operation(Tx{Consequences: consequences, Evidence: images})
	}, images)
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()

	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != status {
		t.Fatalf("error = %v, want HTTP status %d", err, status)
	}
}

// fixture is a closed warning report about subject, filed by reporter.
type fixture struct {
	repo         *fakeRepository
	consequences *fakeConsequences
	service      *Service

	reporter, subject, admin uuid.UUID
	reportID, requestID      uuid.UUID
}

func newFixture(outcome ReportOutcome) fixture {
	f := fixture{
		repo:         newFakeRepository(),
		consequences: &fakeConsequences{},
		reporter:     uuid.New(),
		subject:      uuid.New(),
		admin:        uuid.New(),
		reportID:     uuid.New(),
		requestID:    uuid.New(),
	}
	f.service = newTestService(f.repo, f.consequences)
	f.repo.reports[f.reportID] = ReportFacts{
		ReporterID: f.reporter,
		SubjectID:  f.subject,
		Status:     string(StatusClosed),
		Outcome:    outcome,
		RequestID:  f.requestID,
	}
	return f
}

func (f fixture) submit(t *testing.T, appellant uuid.UUID, targets ...Target) (Appeal, error) {
	t.Helper()
	return f.service.SubmitAppeal(context.Background(), appellant, Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "this was unfair",
		Targets:        targets,
	})
}

func TestSubmitAppealEligibility(t *testing.T) {
	ctx := context.Background()

	t.Run("the reported participant appeals a report outcome", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		appeal, err := f.submit(t, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		if err != nil {
			t.Fatal(err)
		}
		if appeal.Status != StatusOpen || appeal.RequestID != f.requestID || len(appeal.Decisions) != 1 ||
			appeal.Decisions[0].CounterpartID != f.reporter {
			t.Fatalf("appeal = %+v", appeal)
		}
	})

	t.Run("the reporter cannot appeal a consequence for the subject", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		_, err := f.submit(t, f.reporter, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		wantStatus(t, err, http.StatusForbidden)
	})

	t.Run("an unrelated user sees not found", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		_, err := f.submit(t, uuid.New(), Target{Kind: KindReportOutcome, TargetID: f.reportID})
		wantStatus(t, err, http.StatusNotFound)
	})

	t.Run("an unclosed report cannot be appealed", func(t *testing.T) {
		f := newFixture("")
		report := f.repo.reports[f.reportID]
		report.Status = string(StatusUnderReview)
		f.repo.reports[f.reportID] = report
		_, err := f.submit(t, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		wantStatus(t, err, http.StatusPreconditionFailed)
	})

	t.Run("the reporter appeals a no-action outcome on an order", func(t *testing.T) {
		f := newFixture(ReportOutcomeNoAction)
		appeal, err := f.submit(t, f.reporter, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		if err != nil {
			t.Fatal(err)
		}
		if appeal.Decisions[0].CounterpartID != f.subject {
			t.Fatalf("counterpart = %v, want the subject", appeal.Decisions[0].CounterpartID)
		}
	})

	t.Run("a no-action outcome on a review is appealed as the retained review", func(t *testing.T) {
		f := newFixture(ReportOutcomeNoAction)
		report := f.repo.reports[f.reportID]
		report.ReviewID = uuid.New()
		f.repo.reports[f.reportID] = report
		_, err := f.submit(t, f.reporter, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		wantStatus(t, err, http.StatusPreconditionFailed)
	})

	t.Run("warnings", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		own, inactive, fromReport := uuid.New(), uuid.New(), uuid.New()
		f.repo.warnings[own] = WarningFacts{UserID: f.subject, RequestID: f.requestID, Active: true}
		f.repo.warnings[inactive] = WarningFacts{UserID: f.subject, RequestID: f.requestID}
		f.repo.warnings[fromReport] = WarningFacts{UserID: f.subject, ReportID: f.reportID, Active: true}

		if _, err := f.submit(t, f.subject, Target{Kind: KindWarning, TargetID: own}); err != nil {
			t.Fatal(err)
		}
		_, err := f.submit(t, f.reporter, Target{Kind: KindWarning, TargetID: own})
		wantStatus(t, err, http.StatusNotFound)
		_, err = f.submit(t, f.subject, Target{Kind: KindWarning, TargetID: inactive})
		wantStatus(t, err, http.StatusPreconditionFailed)
		_, err = f.submit(t, f.subject, Target{Kind: KindWarning, TargetID: fromReport})
		wantStatus(t, err, http.StatusPreconditionFailed)
	})

	t.Run("review removals and retentions", func(t *testing.T) {
		f := newFixture(ReportOutcomeNoAction)
		author, reviewee := uuid.New(), uuid.New()
		removed, live, byReport := uuid.New(), uuid.New(), uuid.New()
		f.repo.reviews[removed] = ReviewFacts{
			ReviewerID: author, RevieweeID: reviewee, Removed: true, HasUnrestoredRemoval: true,
		}
		f.repo.reviews[live] = ReviewFacts{ReviewerID: author, RevieweeID: reviewee, RetainedByReport: true}
		f.repo.reviews[byReport] = ReviewFacts{
			ReviewerID: author, RevieweeID: reviewee, Removed: true, HasUnrestoredRemoval: true, RemovedByReport: true,
		}

		appeal, err := f.submit(t, author, Target{Kind: KindReviewRemoval, TargetID: removed})
		if err != nil || appeal.Decisions[0].CounterpartID != reviewee {
			t.Fatalf("removal appeal = %+v, err = %v", appeal, err)
		}
		_, err = f.submit(t, reviewee, Target{Kind: KindReviewRemoval, TargetID: removed})
		wantStatus(t, err, http.StatusForbidden)
		_, err = f.submit(t, author, Target{Kind: KindReviewRemoval, TargetID: live})
		wantStatus(t, err, http.StatusPreconditionFailed)
		_, err = f.submit(t, author, Target{Kind: KindReviewRemoval, TargetID: byReport})
		wantStatus(t, err, http.StatusPreconditionFailed)

		appeal, err = f.submit(t, reviewee, Target{Kind: KindReviewRetention, TargetID: live})
		if err != nil || appeal.Decisions[0].CounterpartID != author {
			t.Fatalf("retention appeal = %+v, err = %v", appeal, err)
		}
		_, err = f.submit(t, reviewee, Target{Kind: KindReviewRetention, TargetID: removed})
		wantStatus(t, err, http.StatusPreconditionFailed)
	})

	t.Run("a decision already under appeal is rejected", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		target := Target{Kind: KindReportOutcome, TargetID: f.reportID}
		if _, err := f.submit(t, f.subject, target); err != nil {
			t.Fatal(err)
		}
		_, err := f.submit(t, f.subject, target)
		wantStatus(t, err, http.StatusConflict)
	})

	t.Run("invalid submissions", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		target := Target{Kind: KindReportOutcome, TargetID: f.reportID}
		_, err := f.submit(t, f.subject)
		wantStatus(t, err, http.StatusBadRequest)
		_, err = f.submit(t, f.subject, target, target)
		wantStatus(t, err, http.StatusBadRequest)
		_, err = f.submit(t, f.subject, Target{Kind: KindCreditAdjustment, TargetID: uuid.New()})
		wantStatus(t, err, http.StatusBadRequest)
		_, err = f.service.SubmitAppeal(ctx, f.subject, Submission{
			IdempotencyKey: uuid.New(), Reason: " ", Targets: []Target{target},
		})
		wantStatus(t, err, http.StatusBadRequest)
		_, err = f.service.SubmitAppeal(ctx, f.subject, Submission{
			IdempotencyKey: uuid.New(), RequestID: uuid.New(), Reason: "x", Targets: []Target{target},
		})
		wantStatus(t, err, http.StatusBadRequest)
	})
}

func TestSubmitAppealIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(ReportOutcomeSuspension)
	submission := Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "unfair",
		Targets:        []Target{{Kind: KindReportOutcome, TargetID: f.reportID}},
	}

	first, err := f.service.SubmitAppeal(ctx, f.subject, submission)
	if err != nil {
		t.Fatal(err)
	}
	submission.Reason = "  unfair  "
	second, err := f.service.SubmitAppeal(ctx, f.subject, submission)
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	if second.ID != first.ID || len(f.repo.appeals) != 1 {
		t.Fatalf("resubmission created another appeal: %v and %v", first.ID, second.ID)
	}

	submission.Reason = "a different reason"
	_, err = f.service.SubmitAppeal(ctx, f.subject, submission)
	wantStatus(t, err, http.StatusConflict)
}

func TestAppealLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(ReportOutcomeWarning)
	appeal, err := f.submit(t, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.service.RequestResponse(ctx, f.admin, appeal.ID, f.reporter)
	wantStatus(t, err, http.StatusPreconditionFailed)
	_, err = f.service.BeginReview(ctx, f.reporter, appeal.ID)
	wantStatus(t, err, http.StatusForbidden)

	if _, err := f.service.BeginReview(ctx, f.admin, appeal.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.RequestResponse(ctx, f.admin, appeal.ID, uuid.New())
	wantStatus(t, err, http.StatusBadRequest)
	_, _, err = f.service.SubmitResponse(ctx, f.reporter, appeal.ID, "too early", nil)
	wantStatus(t, err, http.StatusForbidden)

	got, err := f.service.RequestResponse(ctx, f.admin, appeal.ID, f.reporter)
	if err != nil || got.Status != StatusAwaitingResponse || got.AwaitedUserID != f.reporter {
		t.Fatalf("request response: %+v, %v", got, err)
	}
	_, _, err = f.service.SubmitResponse(ctx, f.subject, appeal.ID, "not asked", nil)
	wantStatus(t, err, http.StatusForbidden)
	_, _, err = f.service.SubmitResponse(ctx, uuid.New(), appeal.ID, "stranger", nil)
	wantStatus(t, err, http.StatusNotFound)

	got, response, err := f.service.SubmitResponse(ctx, f.reporter, appeal.ID, " my side ", nil)
	if err != nil || got.Status != StatusUnderReview || got.AwaitedUserID != uuid.Nil || response.Body != "my side" {
		t.Fatalf("respond: %+v %+v %v", got, response, err)
	}

	decisionID := got.Decisions[0].ID
	_, err = f.service.CloseAppeal(ctx, f.admin, appeal.ID, nil)
	wantStatus(t, err, http.StatusBadRequest)
	_, err = f.service.CloseAppeal(ctx, f.admin, appeal.ID, []Resolution{
		{DecisionID: uuid.New(), Outcome: OutcomeUpheld, Reason: "fair"},
	})
	wantStatus(t, err, http.StatusBadRequest)

	closed, err := f.service.CloseAppeal(ctx, f.admin, appeal.ID, []Resolution{
		{DecisionID: decisionID, Outcome: OutcomeUpheld, Reason: " fair "},
	})
	if err != nil || closed.Status != StatusClosed || closed.Decisions[0].Outcome != OutcomeUpheld ||
		closed.Decisions[0].OutcomeReason != "fair" {
		t.Fatalf("close: %+v, %v", closed, err)
	}
	if len(f.consequences.removedWarnings) != 0 {
		t.Fatal("an upheld decision changed the warning")
	}
	_, err = f.service.CloseAppeal(ctx, f.admin, appeal.ID, []Resolution{
		{DecisionID: decisionID, Outcome: OutcomeOverturned, Reason: "again"},
	})
	wantStatus(t, err, http.StatusPreconditionFailed)
}

func TestCloseAppealOverturns(t *testing.T) {
	ctx := context.Background()

	overturnOne := func(t *testing.T, f fixture, appellant uuid.UUID, target Target) error {
		t.Helper()
		appeal, err := f.submit(t, appellant, target)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.service.CloseAppeal(ctx, f.admin, appeal.ID, []Resolution{
			{DecisionID: appeal.Decisions[0].ID, Outcome: OutcomeOverturned, Reason: "overturned"},
		})
		return err
	}

	t.Run("report warning is removed", func(t *testing.T) {
		f := newFixture(ReportOutcomeWarning)
		warningID := uuid.New()
		if err := overturnOne(t, f, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID}); err == nil {
			t.Fatal("expected a missing report warning to fail the close")
		}

		f = newFixture(ReportOutcomeWarning)
		appeal, err := f.submit(t, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID})
		if err != nil {
			t.Fatal(err)
		}
		f.repo.locked[appeal.ID][0].ReportWarningID = warningID
		if _, err := f.service.CloseAppeal(ctx, f.admin, appeal.ID, []Resolution{
			{DecisionID: appeal.Decisions[0].ID, Outcome: OutcomeOverturned, Reason: "overturned"},
		}); err != nil {
			t.Fatal(err)
		}
		if len(f.consequences.removedWarnings) != 1 || f.consequences.removedWarnings[0].WarningID != warningID ||
			f.consequences.removedWarnings[0].AppealID != appeal.ID {
			t.Fatalf("removed warnings = %+v", f.consequences.removedWarnings)
		}
	})

	t.Run("report suspension is lifted", func(t *testing.T) {
		f := newFixture(ReportOutcomeSuspension)
		if err := overturnOne(t, f, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID}); err != nil {
			t.Fatal(err)
		}
		if len(f.consequences.reinstated) != 1 || f.consequences.reinstated[0].UserID != f.subject ||
			f.consequences.reinstated[0].ReportID != f.reportID {
			t.Fatalf("reinstated = %+v", f.consequences.reinstated)
		}
	})

	t.Run("report review removal is restored", func(t *testing.T) {
		f := newFixture(ReportOutcomeReviewRemoval)
		reviewID := uuid.New()
		report := f.repo.reports[f.reportID]
		report.ReviewID = reviewID
		f.repo.reports[f.reportID] = report
		if err := overturnOne(t, f, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID}); err != nil {
			t.Fatal(err)
		}
		if len(f.consequences.restored) != 1 || f.consequences.restored[0].ReviewID != reviewID {
			t.Fatalf("restored = %+v", f.consequences.restored)
		}
	})

	t.Run("report no action records the disposition only", func(t *testing.T) {
		f := newFixture(ReportOutcomeNoAction)
		if err := overturnOne(t, f, f.reporter, Target{Kind: KindReportOutcome, TargetID: f.reportID}); err != nil {
			t.Fatal(err)
		}
		c := f.consequences
		if len(c.removedWarnings)+len(c.restored)+len(c.removedReviews)+len(c.reinstated) != 0 {
			t.Fatalf("no-action overturn changed something: %+v", c)
		}
	})

	t.Run("warning, review removal and review retention", func(t *testing.T) {
		f := newFixture(ReportOutcomeNoAction)
		author, reviewee := uuid.New(), uuid.New()
		warningID, removed, live := uuid.New(), uuid.New(), uuid.New()
		f.repo.warnings[warningID] = WarningFacts{UserID: author, RequestID: f.requestID, Active: true}
		f.repo.reviews[removed] = ReviewFacts{
			ReviewerID: author, RevieweeID: reviewee, Removed: true, HasUnrestoredRemoval: true,
		}
		f.repo.reviews[live] = ReviewFacts{ReviewerID: reviewee, RevieweeID: author, RetainedByReport: true}

		appeal, err := f.submit(t, author,
			Target{Kind: KindWarning, TargetID: warningID},
			Target{Kind: KindReviewRemoval, TargetID: removed},
			Target{Kind: KindReviewRetention, TargetID: live},
		)
		if err != nil {
			t.Fatal(err)
		}
		resolutions := make([]Resolution, len(appeal.Decisions))
		for i, decision := range appeal.Decisions {
			resolutions[i] = Resolution{DecisionID: decision.ID, Outcome: OutcomeOverturned, Reason: "overturned"}
		}
		if _, err := f.service.CloseAppeal(ctx, f.admin, appeal.ID, resolutions); err != nil {
			t.Fatal(err)
		}
		c := f.consequences
		if len(c.removedWarnings) != 1 || c.removedWarnings[0].WarningID != warningID ||
			len(c.restored) != 1 || c.restored[0].ReviewID != removed ||
			len(c.removedReviews) != 1 || c.removedReviews[0] != live {
			t.Fatalf("consequences = %+v", c)
		}
	})

	t.Run("a failing consequence fails the close", func(t *testing.T) {
		f := newFixture(ReportOutcomeSuspension)
		f.consequences.err = errors.New("role change failed")
		if err := overturnOne(t, f, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID}); err == nil {
			t.Fatal("expected the close to fail")
		}
	})
}

func TestAppealVisibility(t *testing.T) {
	ctx := context.Background()
	f := newFixture(ReportOutcomeWarning)
	appeal, err := f.submit(t, f.subject, Target{Kind: KindReportOutcome, TargetID: f.reportID})
	if err != nil {
		t.Fatal(err)
	}

	for _, caller := range []uuid.UUID{f.subject, f.reporter} {
		if _, err := f.service.GetAppeal(ctx, caller, false, appeal.ID); err != nil {
			t.Fatalf("participant %v: %v", caller, err)
		}
		page, err := f.service.ListMyAppeals(ctx, caller, pagination.Pagination{})
		if err != nil || len(page.Appeals) != 1 {
			t.Fatalf("list for %v: %+v, %v", caller, page, err)
		}
	}
	if _, err := f.service.GetAppeal(ctx, f.admin, true, appeal.ID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	_, err = f.service.GetAppeal(ctx, uuid.New(), false, appeal.ID)
	wantStatus(t, err, http.StatusNotFound)
}

func TestAppealEvidence(t *testing.T) {
	ctx := context.Background()
	f := newFixture(ReportOutcomeWarning)
	images := newFakeEvidence()
	f.service = newTestServiceWithEvidence(f.repo, f.consequences, images)

	// The appellant's images are attached to the appeal (RP8.2).
	image, _, _ := f.service.CreateEvidenceUpload(ctx, f.subject, "image/png", 10)
	submission := Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "see the photo",
		Targets:        []Target{{Kind: KindReportOutcome, TargetID: f.reportID}},
		EvidenceIDs:    []uuid.UUID{image},
	}
	submitted, err := f.service.SubmitAppeal(ctx, f.subject, submission)
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Evidence) != 1 || submitted.Evidence[0].ID != image {
		t.Fatalf("appeal evidence = %+v", submitted.Evidence)
	}

	// A retry replays even though its images are no longer pending (RP7.7).
	again, err := f.service.SubmitAppeal(ctx, f.subject, submission)
	if err != nil || again.ID != submitted.ID {
		t.Fatalf("retry = %v, %v; want %v", again.ID, err, submitted.ID)
	}

	// The counterpart replies with images when asked (RP9.4).
	if _, err := f.service.BeginReview(ctx, f.admin, submitted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RequestResponse(ctx, f.admin, submitted.ID, f.reporter); err != nil {
		t.Fatal(err)
	}
	reply, _, _ := f.service.CreateEvidenceUpload(ctx, f.reporter, "image/png", 10)
	_, response, err := f.service.SubmitResponse(ctx, f.reporter, submitted.ID, "my photo", []uuid.UUID{reply})
	if err != nil || len(response.Evidence) != 1 || response.Evidence[0].ID != reply {
		t.Fatalf("response = %+v, %v", response, err)
	}

	// Downloads follow the appeal's visibility.
	if _, err := f.service.GetEvidenceDownload(ctx, f.reporter, false, image); err != nil {
		t.Fatalf("counterpart download: %v", err)
	}
	_, err = f.service.GetEvidenceDownload(ctx, uuid.New(), false, image)
	wantStatus(t, err, http.StatusNotFound)
}
