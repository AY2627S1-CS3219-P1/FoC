package report

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/google/uuid"
)

// fakeRepository keeps reports in memory and enforces the unclosed-report
// uniqueness the database provides.
type fakeRepository struct {
	users     map[uuid.UUID]bool
	reviews   map[uuid.UUID]uuid.UUID // live review -> author
	reports   map[uuid.UUID]Report
	responses []Response
	requestOf map[uuid.UUID]uuid.UUID // review report -> originating order
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		users:     map[uuid.UUID]bool{},
		reviews:   map[uuid.UUID]uuid.UUID{},
		reports:   map[uuid.UUID]Report{},
		requestOf: map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeRepository) UserExists(_ context.Context, id uuid.UUID) (bool, error) {
	return f.users[id], nil
}

func (f *fakeRepository) LiveReviewAuthor(_ context.Context, reviewID uuid.UUID) (uuid.UUID, error) {
	author, ok := f.reviews[reviewID]
	if !ok {
		return uuid.Nil, errReviewNotFound
	}
	return author, nil
}

func (f *fakeRepository) InsertReport(_ context.Context, _ *reportdb.Queries, input ReportInput) error {
	for _, existing := range f.reports {
		if existing.Status.IsClosed() || existing.ReporterID != input.ReporterID {
			continue
		}
		sameReview := input.ReviewID != uuid.Nil && existing.ReviewID == input.ReviewID
		sameOrder := input.OrderID != uuid.Nil && existing.OrderID == input.OrderID &&
			existing.SubjectID == input.SubjectID
		if sameReview || sameOrder {
			return errReportAlreadyOpen
		}
	}

	f.reports[input.ID] = Report{
		ID:          input.ID,
		ReporterID:  input.ReporterID,
		SubjectID:   input.SubjectID,
		ReviewID:    input.ReviewID,
		OrderID:     input.OrderID,
		Category:    input.Category,
		Description: input.Description,
		Status:      StatusOpen,
	}
	return nil
}

func (f *fakeRepository) GetReport(_ context.Context, id uuid.UUID) (Report, error) {
	report, ok := f.reports[id]
	if !ok {
		return Report{}, errReportNotFound
	}
	return report, nil
}

func (f *fakeRepository) ListResponses(_ context.Context, reportID uuid.UUID) ([]Response, error) {
	var out []Response
	for _, response := range f.responses {
		if response.ReportID == reportID {
			out = append(out, response)
		}
	}
	return out, nil
}

func (f *fakeRepository) ListReportsInvolving(_ context.Context, userID uuid.UUID, _, _ int64) ([]Report, error) {
	var out []Report
	for _, report := range f.reports {
		if report.Involves(userID) {
			out = append(out, report)
		}
	}
	return out, nil
}

func (f *fakeRepository) CountReportsInvolving(ctx context.Context, userID uuid.UUID) (int64, error) {
	reports, _ := f.ListReportsInvolving(ctx, userID, 0, 0)
	return int64(len(reports)), nil
}

func (f *fakeRepository) ListReports(_ context.Context, filter Filter, _, _ int64) ([]Report, error) {
	var out []Report
	for _, report := range f.reports {
		if filter.Status == "" || report.Status == filter.Status {
			out = append(out, report)
		}
	}
	return out, nil
}

func (f *fakeRepository) CountReports(ctx context.Context, filter Filter) (int64, error) {
	reports, _ := f.ListReports(ctx, filter, 0, 0)
	return int64(len(reports)), nil
}

func (f *fakeRepository) LockReport(_ context.Context, _ *reportdb.Queries, id uuid.UUID) (LockedReport, error) {
	report, ok := f.reports[id]
	if !ok {
		return LockedReport{}, errReportNotFound
	}

	requestID := report.OrderID
	if requestID == uuid.Nil {
		requestID = f.requestOf[id]
	}
	return LockedReport{
		ReporterID: report.ReporterID,
		SubjectID:  report.SubjectID,
		ReviewID:   report.ReviewID,
		Status:     report.Status,
		RequestID:  requestID,
	}, nil
}

func (f *fakeRepository) TransitionReport(
	_ context.Context,
	_ *reportdb.Queries,
	id uuid.UUID,
	from, to Status,
) error {
	report := f.reports[id]
	if report.Status != from {
		return errReportStateChanged
	}
	report.Status = to
	f.reports[id] = report
	return nil
}

func (f *fakeRepository) CloseReport(_ context.Context, _ *reportdb.Queries, id uuid.UUID, closure Closure) error {
	report := f.reports[id]
	if report.Status.IsClosed() {
		return errReportStateChanged
	}
	report.Status = StatusClosed
	report.Outcome = closure.Outcome
	report.OutcomeReason = closure.Reason
	report.ClosedBy = closure.ClosedBy
	f.reports[id] = report
	return nil
}

func (f *fakeRepository) InsertResponse(_ context.Context, _ *reportdb.Queries, input ResponseInput) error {
	f.responses = append(f.responses, Response{
		ID:          input.ID,
		ReportID:    input.ReportID,
		ResponderID: input.ResponderID,
		Body:        input.Body,
	})
	return nil
}

// fakeConsequences records the outcome applied by CloseReport.
type fakeConsequences struct {
	warnings    []Warning
	suspensions []Suspension
	removed     []uuid.UUID
	err         error
}

func (f *fakeConsequences) RecordWarning(_ context.Context, warning Warning) error {
	f.warnings = append(f.warnings, warning)
	return f.err
}

func (f *fakeConsequences) Suspend(_ context.Context, suspension Suspension) error {
	f.suspensions = append(f.suspensions, suspension)
	return f.err
}

func (f *fakeConsequences) RemoveReview(_ context.Context, _, reviewID uuid.UUID, _ string) error {
	f.removed = append(f.removed, reviewID)
	return f.err
}

// newTestService runs each transaction against the fakes. The fake repository
// does not roll back, so tests of failing transactions check only the error.
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
	return id, images.UploadGrant{URL: "https://storage/" + id.String()}, nil
}

func (f *fakeEvidence) Prepare(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]evidence.Prepared, error) {
	prepared := make([]evidence.Prepared, 0, len(ids))
	for _, id := range ids {
		if !f.staged[id] {
			return nil, errs.NewBadRequestError("image was not staged")
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
			views[parent] = append(views[parent], evidence.View{ID: id, URL: "https://storage/" + id.String()})
		}
	}
	return views, nil
}

func (f *fakeEvidence) Locate(_ context.Context, id uuid.UUID) (evidence.Owner, error) {
	for parent, ids := range f.attached {
		if parent.Kind == evidence.ParentReport && slices.Contains(ids, id) {
			return evidence.Owner{ReportID: parent.ID}, nil
		}
	}
	return evidence.Owner{}, errs.NewNotFoundError("evidence not found")
}

func (f *fakeEvidence) Download(_ context.Context, id uuid.UUID) (evidence.View, error) {
	return evidence.View{ID: id, URL: "https://storage/" + id.String() + "?fresh"}, nil
}

func (f *fakeEvidence) Attach(
	_ context.Context,
	_ uuid.UUID,
	parent evidence.Parent,
	prepared []evidence.Prepared,
) error {
	for _, image := range prepared {
		f.attached[parent] = append(f.attached[parent], image.ID)
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

// Submission

func TestSubmitReport(t *testing.T) {
	reporter, author, other := uuid.New(), uuid.New(), uuid.New()
	liveReview, ownReview := uuid.New(), uuid.New()

	newRepo := func() *fakeRepository {
		repo := newFakeRepository()
		repo.users[reporter], repo.users[author], repo.users[other] = true, true, true
		repo.reviews[liveReview] = author
		repo.reviews[ownReview] = reporter
		return repo
	}
	submission := func(target Target) Submission {
		return Submission{Target: target, Category: CategoryHarassment, Description: "  rude comment  "}
	}

	t.Run("review target derives the subject from the author", func(t *testing.T) {
		got, err := newTestService(newRepo(), nil).SubmitReport(context.Background(), reporter,
			submission(ReviewTarget(liveReview)))
		if err != nil {
			t.Fatal(err)
		}
		if got.SubjectID != author || got.ReviewID != liveReview || got.OrderID != uuid.Nil {
			t.Fatalf("report = %+v", got)
		}
		if got.Description != "rude comment" || got.Status != StatusOpen {
			t.Fatalf("description or status not normalized: %+v", got)
		}
	})

	t.Run("order target keeps the named subject", func(t *testing.T) {
		order := uuid.New()
		got, err := newTestService(newRepo(), nil).SubmitReport(context.Background(), reporter,
			submission(OrderTarget(order, other)))
		if err != nil {
			t.Fatal(err)
		}
		if got.SubjectID != other || got.OrderID != order || got.ReviewID != uuid.Nil {
			t.Fatalf("report = %+v", got)
		}
	})

	for _, tc := range []struct {
		name       string
		submission Submission
		status     int
	}{
		{"own review", submission(ReviewTarget(ownReview)), http.StatusBadRequest},
		{"missing or removed review", submission(ReviewTarget(uuid.New())), http.StatusNotFound},
		{"self on order", submission(OrderTarget(uuid.New(), reporter)), http.StatusBadRequest},
		{"missing order subject", submission(OrderTarget(uuid.New(), uuid.New())), http.StatusNotFound},
		{"no target", submission(Target{}), http.StatusBadRequest},
		{
			"both targets",
			submission(Target{ReviewID: liveReview, OrderID: uuid.New(), SubjectID: other}),
			http.StatusBadRequest,
		},
		{
			"blank description",
			Submission{Target: ReviewTarget(liveReview), Category: CategoryFraud, Description: "   "},
			http.StatusBadRequest,
		},
		{
			"long description",
			Submission{Target: ReviewTarget(liveReview), Category: CategoryFraud, Description: strings.Repeat("a", 4001)},
			http.StatusBadRequest,
		},
		{
			"unknown category",
			Submission{Target: ReviewTarget(liveReview), Category: "spam", Description: "x"},
			http.StatusBadRequest,
		},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			repo := newRepo()
			_, err := newTestService(repo, nil).SubmitReport(context.Background(), reporter, tc.submission)
			wantStatus(t, err, tc.status)
			if len(repo.reports) != 0 {
				t.Fatalf("rejected report was stored: %+v", repo.reports)
			}
		})
	}

	t.Run("rejects a second unclosed report on the same target", func(t *testing.T) {
		service := newTestService(newRepo(), nil)
		_, err := service.SubmitReport(context.Background(), reporter, submission(ReviewTarget(liveReview)))
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.SubmitReport(context.Background(), reporter, submission(ReviewTarget(liveReview)))
		wantStatus(t, err, http.StatusConflict)
	})
}

// Visibility

func TestGetReportVisibility(t *testing.T) {
	reporter, subject, stranger := uuid.New(), uuid.New(), uuid.New()
	repo := newFakeRepository()
	id := uuid.New()
	repo.reports[id] = Report{ID: id, ReporterID: reporter, SubjectID: subject, Status: StatusOpen}
	service := newTestService(repo, nil)

	// Both participants and administrators can read the report (RP4.1, RP4.2).
	for name, caller := range map[string]uuid.UUID{"reporter": reporter, "subject": subject} {
		if _, err := service.GetReport(context.Background(), caller, false, id); err != nil {
			t.Fatalf("%s denied: %v", name, err)
		}
	}
	if _, err := service.GetReport(context.Background(), stranger, true, id); err != nil {
		t.Fatalf("admin denied: %v", err)
	}

	_, err := service.GetReport(context.Background(), stranger, false, id)
	wantStatus(t, err, http.StatusNotFound)
}

// Lifecycle

type lifecycleFixture struct {
	repo         *fakeRepository
	consequences *fakeConsequences
	service      *Service
	id           uuid.UUID
	reporter     uuid.UUID
	subject      uuid.UUID
	admin        uuid.UUID
	reviewID     uuid.UUID
	orderID      uuid.UUID
}

// newLifecycle stores one report in status. A review report records the
// review's order as the originating request.
func newLifecycle(status Status, aboutReview bool) lifecycleFixture {
	f := lifecycleFixture{
		repo:         newFakeRepository(),
		consequences: &fakeConsequences{},
		id:           uuid.New(),
		reporter:     uuid.New(),
		subject:      uuid.New(),
		admin:        uuid.New(),
		orderID:      uuid.New(),
	}
	report := Report{ID: f.id, ReporterID: f.reporter, SubjectID: f.subject, Status: status}
	if aboutReview {
		f.reviewID = uuid.New()
		report.ReviewID = f.reviewID
		f.repo.requestOf[f.id] = f.orderID
	} else {
		report.OrderID = f.orderID
	}
	f.repo.reports[f.id] = report
	f.service = newTestService(f.repo, f.consequences)
	return f
}

func (f lifecycleFixture) status() Status { return f.repo.reports[f.id].Status }

func TestReportLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("review, request response, respond, close", func(t *testing.T) {
		f := newLifecycle(StatusOpen, false)

		if _, err := f.service.BeginReview(ctx, f.admin, f.id); err != nil || f.status() != StatusUnderReview {
			t.Fatalf("begin review: status=%s err=%v", f.status(), err)
		}
		if _, err := f.service.RequestResponse(ctx, f.admin, f.id); err != nil ||
			f.status() != StatusAwaitingResponse {
			t.Fatalf("request response: status=%s err=%v", f.status(), err)
		}

		_, response, err := f.service.SubmitResponse(ctx, f.subject, f.id, "  it was a misunderstanding ", nil)
		if err != nil {
			t.Fatal(err)
		}
		if f.status() != StatusUnderReview || response.Body != "it was a misunderstanding" {
			t.Fatalf("respond: status=%s response=%+v", f.status(), response)
		}

		closed, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeNoAction, "both at fault")
		if err != nil {
			t.Fatal(err)
		}
		if closed.Status != StatusClosed || closed.Outcome != OutcomeNoAction ||
			closed.OutcomeReason != "both at fault" {
			t.Fatalf("closed = %+v", closed)
		}
	})

	t.Run("an unrequested response keeps the status", func(t *testing.T) {
		f := newLifecycle(StatusOpen, false)
		if _, _, err := f.service.SubmitResponse(ctx, f.subject, f.id, "context", nil); err != nil {
			t.Fatal(err)
		}
		if f.status() != StatusOpen {
			t.Fatalf("status = %s, want open", f.status())
		}
	})

	for _, tc := range []struct {
		name   string
		status Status
		act    func(f lifecycleFixture) error
	}{
		{"begin review twice", StatusUnderReview, func(f lifecycleFixture) error {
			_, err := f.service.BeginReview(ctx, f.admin, f.id)
			return err
		}},
		{"request response before review", StatusOpen, func(f lifecycleFixture) error {
			_, err := f.service.RequestResponse(ctx, f.admin, f.id)
			return err
		}},
		{"respond after close", StatusClosed, func(f lifecycleFixture) error {
			_, _, err := f.service.SubmitResponse(ctx, f.subject, f.id, "late", nil)
			return err
		}},
		{"close twice", StatusClosed, func(f lifecycleFixture) error {
			_, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeNoAction, "again")
			return err
		}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			f := newLifecycle(tc.status, false)
			wantStatus(t, tc.act(f), http.StatusPreconditionFailed)
			if f.status() != tc.status {
				t.Fatalf("status changed to %s", f.status())
			}
		})
	}

	t.Run("only the reported participant may respond", func(t *testing.T) {
		f := newLifecycle(StatusAwaitingResponse, false)
		_, _, err := f.service.SubmitResponse(ctx, f.reporter, f.id, "me too", nil)
		wantStatus(t, err, http.StatusForbidden)
		_, _, err = f.service.SubmitResponse(ctx, uuid.New(), f.id, "stranger", nil)
		wantStatus(t, err, http.StatusNotFound)
		if len(f.repo.responses) != 0 {
			t.Fatalf("rejected responses stored: %+v", f.repo.responses)
		}
	})

	t.Run("administrators cannot act on reports they are party to", func(t *testing.T) {
		f := newLifecycle(StatusOpen, false)
		for _, actor := range []uuid.UUID{f.reporter, f.subject} {
			_, err := f.service.BeginReview(ctx, actor, f.id)
			wantStatus(t, err, http.StatusForbidden)
			_, err = f.service.CloseReport(ctx, actor, f.id, OutcomeNoAction, "mine")
			wantStatus(t, err, http.StatusForbidden)
		}
	})

	t.Run("rejects a missing report", func(t *testing.T) {
		f := newLifecycle(StatusOpen, false)
		_, err := f.service.BeginReview(ctx, f.admin, uuid.New())
		wantStatus(t, err, http.StatusNotFound)
	})
}

// Outcomes

func TestCloseReportOutcomes(t *testing.T) {
	ctx := context.Background()

	t.Run("warning records the originating request", func(t *testing.T) {
		for _, aboutReview := range []bool{false, true} {
			f := newLifecycle(StatusUnderReview, aboutReview)
			if _, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeWarning, "rude"); err != nil {
				t.Fatal(err)
			}
			want := Warning{UserID: f.subject, RequestID: f.orderID, ReportID: f.id, ActorID: f.admin, Reason: "rude"}
			if len(f.consequences.warnings) != 1 || f.consequences.warnings[0] != want {
				t.Fatalf("review=%v warnings = %+v, want %+v", aboutReview, f.consequences.warnings, want)
			}
		}
	})

	t.Run("suspension targets the subject", func(t *testing.T) {
		f := newLifecycle(StatusUnderReview, false)
		if _, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeSuspension, "repeated fraud"); err != nil {
			t.Fatal(err)
		}
		want := Suspension{UserID: f.subject, ReportID: f.id, ActorID: f.admin, Reason: "repeated fraud"}
		if len(f.consequences.suspensions) != 1 || f.consequences.suspensions[0] != want {
			t.Fatalf("suspensions = %+v", f.consequences.suspensions)
		}
	})

	t.Run("review removal removes the reported review", func(t *testing.T) {
		f := newLifecycle(StatusUnderReview, true)
		if _, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeReviewRemoval, "abusive"); err != nil {
			t.Fatal(err)
		}
		if len(f.consequences.removed) != 1 || f.consequences.removed[0] != f.reviewID {
			t.Fatalf("removed = %+v", f.consequences.removed)
		}
	})

	t.Run("review removal needs a review target", func(t *testing.T) {
		f := newLifecycle(StatusUnderReview, false)
		_, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeReviewRemoval, "abusive")
		wantStatus(t, err, http.StatusPreconditionFailed)
		if len(f.consequences.removed) != 0 || f.status() != StatusUnderReview {
			t.Fatal("rejected close applied a consequence")
		}
	})

	t.Run("no action applies nothing", func(t *testing.T) {
		f := newLifecycle(StatusOpen, true)
		if _, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeNoAction, "unfounded"); err != nil {
			t.Fatal(err)
		}
		c := f.consequences
		if len(c.warnings)+len(c.suspensions)+len(c.removed) != 0 {
			t.Fatalf("consequences = %+v", c)
		}
	})

	t.Run("a failed consequence fails the close", func(t *testing.T) {
		f := newLifecycle(StatusUnderReview, false)
		f.consequences.err = errors.New("role conflict")
		if _, err := f.service.CloseReport(ctx, f.admin, f.id, OutcomeSuspension, "fraud"); err == nil {
			t.Fatal("expected the consequence error")
		}
	})

	for _, tc := range []struct {
		name    string
		outcome Outcome
		reason  string
	}{
		{"unknown outcome", "credit_adjustment", "reason"},
		{"blank reason", OutcomeNoAction, "  "},
		{"long reason", OutcomeNoAction, strings.Repeat("r", 2001)},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			f := newLifecycle(StatusUnderReview, false)
			_, err := f.service.CloseReport(ctx, f.admin, f.id, tc.outcome, tc.reason)
			wantStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestListMyReportsIncludesBothRoles(t *testing.T) {
	user := uuid.New()
	repo := newFakeRepository()
	filed, about, unrelated := uuid.New(), uuid.New(), uuid.New()
	repo.reports[filed] = Report{ID: filed, ReporterID: user, SubjectID: uuid.New(), Status: StatusOpen}
	repo.reports[about] = Report{ID: about, ReporterID: uuid.New(), SubjectID: user, Status: StatusClosed}
	repo.reports[unrelated] = Report{ID: unrelated, ReporterID: uuid.New(), SubjectID: uuid.New(), Status: StatusOpen}

	page, err := pagination.New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := newTestService(repo, nil).ListMyReports(context.Background(), user, page)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalItems() != 2 || got.TotalPages() != 2 {
		t.Fatalf("page info = %+v", got.PageInfo)
	}
}

func TestReportEvidence(t *testing.T) {
	ctx := context.Background()
	reporter, subject := uuid.New(), uuid.New()
	repo := newFakeRepository()
	repo.users[reporter], repo.users[subject] = true, true
	images := newFakeEvidence()
	service := newTestServiceWithEvidence(repo, &fakeConsequences{}, images)

	// Submission attaches the reporter's images in order (RP1.6).
	first, _, _ := service.CreateEvidenceUpload(ctx, reporter, "image/png", 10)
	second, _, _ := service.CreateEvidenceUpload(ctx, reporter, "image/png", 10)
	submitted, err := service.SubmitReport(ctx, reporter, Submission{
		Target:      OrderTarget(uuid.New(), subject),
		Category:    CategoryNoShow,
		Description: "never arrived",
		EvidenceIDs: []uuid.UUID{second, first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Evidence) != 2 || submitted.Evidence[0].ID != second || submitted.Evidence[1].ID != first {
		t.Fatalf("report evidence = %+v", submitted.Evidence)
	}

	// An image that was never staged fails the submission before any write.
	_, err = service.SubmitReport(ctx, reporter, Submission{
		Target:      OrderTarget(uuid.New(), subject),
		Category:    CategoryNoShow,
		Description: "again",
		EvidenceIDs: []uuid.UUID{uuid.New()},
	})
	wantStatus(t, err, http.StatusBadRequest)

	// The reported participant's reply carries its own images (RP2.2).
	reply, _, _ := service.CreateEvidenceUpload(ctx, subject, "image/jpeg", 10)
	_, response, err := service.SubmitResponse(ctx, subject, submitted.ID, "here is proof", []uuid.UUID{reply})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Evidence) != 1 || response.Evidence[0].ID != reply {
		t.Fatalf("response evidence = %+v", response.Evidence)
	}

	// Only those who can read the report can download its images.
	if view, err := service.GetEvidenceDownload(ctx, subject, false, first); err != nil || view.ID != first {
		t.Fatalf("participant download = %+v, %v", view, err)
	}
	if _, err := service.GetEvidenceDownload(ctx, uuid.New(), true, first); err != nil {
		t.Fatalf("admin download: %v", err)
	}
	_, err = service.GetEvidenceDownload(ctx, uuid.New(), false, first)
	wantStatus(t, err, http.StatusNotFound)
}
