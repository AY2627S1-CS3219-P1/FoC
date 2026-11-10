package report_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/orders"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/testsupport"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
)

type integration struct {
	t         *testing.T
	ctx       context.Context
	db        *sql.DB
	service   *report.Service
	orders    testOrders
	directory *testsupport.Directory
	admin     uuid.UUID
}

func setupIntegration(t *testing.T) integration {
	t.Helper()
	db := testsupport.Database(t)
	directory := testsupport.NewDirectory()
	known := testOrders{}
	return integration{
		t:   t,
		ctx: context.Background(),
		db:  db,
		// Wired as the composition root does, with known orders in place of
		// Order Service and an in-memory User Service.
		service: report.NewService(
			report.NewPostgresRepository(reportdb.New(db)),
			outbox.ReportTransaction(db),
			evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(db)), nil),
			known,
			directory,
		),
		orders:    known,
		directory: directory,
		admin:     directory.User("Admin", tokenclaims.RoleAdmin),
	}
}

func (it integration) user(name string) uuid.UUID {
	return it.directory.User(name, tokenclaims.RoleUser)
}

func (it integration) submit(reporter uuid.UUID, target report.Target) report.Report {
	it.t.Helper()
	got, err := it.service.SubmitReport(it.ctx, reporter, report.Submission{
		Target:      target,
		Category:    report.CategoryHarassment,
		Description: "inappropriate behaviour",
	})
	if err != nil {
		it.t.Fatalf("submit report: %v", err)
	}
	return got
}

func (it integration) count(query string, args ...any) int {
	it.t.Helper()
	var n int
	if err := it.db.QueryRowContext(it.ctx, query, args...).Scan(&n); err != nil {
		it.t.Fatal(err)
	}
	return n
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != status {
		t.Fatalf("error = %v, want HTTP status %d", err, status)
	}
}

func TestReportLifecycleIntegration(t *testing.T) {
	it := setupIntegration(t)
	firstPage, err := pagination.New(1, 20)
	if err != nil {
		t.Fatal(err)
	}

	requester := it.user("Requester")
	courier := it.user("Courier")
	orderID := it.order(requester, courier)

	// Submission and participant visibility (RP1, RP4)

	submitted := it.submit(requester, report.OrderTarget(orderID, courier))
	if submitted.Status != report.StatusOpen || submitted.SubjectName != "Courier" {
		t.Fatalf("submitted = %+v", submitted)
	}
	_, err = it.service.SubmitReport(it.ctx, requester, report.Submission{
		Target:      report.OrderTarget(orderID, courier),
		Category:    report.CategoryOther,
		Description: "again",
	})
	wantStatus(t, err, http.StatusConflict)

	for _, participant := range []uuid.UUID{requester, courier} {
		mine, err := it.service.ListMyReports(it.ctx, participant, firstPage)
		if err != nil || mine.TotalItems() != 1 || mine.Reports[0].ID != submitted.ID {
			t.Fatalf("participant %s reports = %+v, err = %v", participant, mine, err)
		}
	}

	// Review, response request and response (RP2, RP3)

	if _, err := it.service.BeginReview(it.ctx, it.admin, submitted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := it.service.RequestResponse(it.ctx, it.admin, submitted.ID); err != nil {
		t.Fatal(err)
	}
	afterResponse, response, err := it.service.SubmitResponse(it.ctx, courier, submitted.ID, "traffic delay", nil)
	if err != nil {
		t.Fatal(err)
	}
	if afterResponse.Status != report.StatusUnderReview || response.ResponderName != "Courier" {
		t.Fatalf("after response: %+v / %+v", afterResponse, response)
	}

	detail, err := it.service.GetReport(it.ctx, requester, false, submitted.ID)
	if err != nil || len(detail.Responses) != 1 || detail.Responses[0].Body != "traffic delay" {
		t.Fatalf("detail = %+v, err = %v", detail, err)
	}
	byStatus, err := it.service.ListReports(it.ctx, report.Filter{Status: report.StatusUnderReview}, firstPage)
	if err != nil || byStatus.TotalItems() != 1 {
		t.Fatalf("status filter = %+v, err = %v", byStatus, err)
	}

	// Warning outcome (RP3.5, RP5.2), recorded for User Service

	closed, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeWarning, "late twice")
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != report.StatusClosed || closed.Outcome != report.OutcomeWarning ||
		closed.OutcomeReason != "late twice" || closed.CloserName != "Admin" || closed.ClosedAt.IsZero() {
		t.Fatalf("closed = %+v", closed)
	}
	outcomes := testsupport.Outcomes(t, it.db, submitted.ID, uuid.Nil)
	if len(outcomes) != 1 || outcomes[0].GetWarnUser().GetUserId() != courier.String() ||
		outcomes[0].GetWarnUser().GetRequestId() != orderID.String() ||
		outcomes[0].GetActorId() != it.admin.String() || outcomes[0].GetReason() != "late twice" {
		t.Fatalf("outcomes = %v", outcomes)
	}

	_, _, err = it.service.SubmitResponse(it.ctx, courier, submitted.ID, "too late", nil)
	wantStatus(t, err, http.StatusPreconditionFailed)

	// A closed report frees the target for a new one.
	it.submit(requester, report.OrderTarget(orderID, courier))
}

func TestReportSuspensionOutcomeIntegration(t *testing.T) {
	it := setupIntegration(t)

	reporter := it.user("Reporter")
	subject := it.user("Subject")
	submitted := it.submit(reporter, report.OrderTarget(it.order(reporter, subject), subject))

	if _, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeSuspension, "fraud"); err != nil {
		t.Fatal(err)
	}
	outcomes := testsupport.Outcomes(t, it.db, submitted.ID, uuid.Nil)
	if len(outcomes) != 1 || outcomes[0].GetSuspendUser().GetUserId() != subject.String() {
		t.Fatalf("outcomes = %v", outcomes)
	}

	// A suspension User Service would reject leaves the report open.
	admin := it.directory.User("Other admin", tokenclaims.RoleAdmin)
	other := it.submit(reporter, report.OrderTarget(it.order(reporter, admin), admin))
	_, err := it.service.CloseReport(it.ctx, it.admin, other.ID, report.OutcomeSuspension, "again")
	wantStatus(t, err, http.StatusPreconditionFailed)
	if n := it.count("SELECT count(*) FROM reports WHERE id = $1 AND status = 'open'", other.ID); n != 1 {
		t.Fatal("rejected suspension still closed the report")
	}
	if len(testsupport.Outcomes(t, it.db, other.ID, uuid.Nil)) != 0 {
		t.Fatal("rejected suspension recorded an outcome")
	}
}

func TestReportReviewRemovalOutcomeIntegration(t *testing.T) {
	it := setupIntegration(t)

	reviewee := it.user("Reviewee")
	author := it.user("Author")
	orderID := uuid.New()
	reviewID := it.directory.Review(author, reviewee, orderID)
	submitted := it.submit(reviewee, report.ReviewTarget(reviewID))
	if submitted.SubjectID != author {
		t.Fatalf("subject = %s, want review author", submitted.SubjectID)
	}

	if _, err := it.service.CloseReport(
		it.ctx, it.admin, submitted.ID, report.OutcomeReviewRemoval, "abusive language",
	); err != nil {
		t.Fatal(err)
	}
	outcomes := testsupport.Outcomes(t, it.db, submitted.ID, uuid.Nil)
	if len(outcomes) != 1 || outcomes[0].GetRemoveReview().GetReviewId() != reviewID.String() {
		t.Fatalf("outcomes = %v", outcomes)
	}

	// Once User Service removes it, the review can no longer be newly reported.
	it.directory.RemoveReview(reviewID, submitted.ID)
	_, err := it.service.SubmitReport(it.ctx, it.user("Other"), report.Submission{
		Target:      report.ReviewTarget(reviewID),
		Category:    report.CategoryOther,
		Description: "late report",
	})
	wantStatus(t, err, http.StatusNotFound)
}

func TestReportWarningUsesReviewOrderIntegration(t *testing.T) {
	it := setupIntegration(t)

	reviewee := it.user("Reviewee")
	author := it.user("Author")
	orderID := uuid.New()
	submitted := it.submit(reviewee, report.ReviewTarget(it.directory.Review(author, reviewee, orderID)))

	if _, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeWarning, "rude"); err != nil {
		t.Fatal(err)
	}
	outcomes := testsupport.Outcomes(t, it.db, submitted.ID, uuid.Nil)
	if len(outcomes) != 1 || outcomes[0].GetWarnUser().GetUserId() != author.String() ||
		outcomes[0].GetWarnUser().GetRequestId() != orderID.String() {
		t.Fatalf("warning did not record the review's order: %v", outcomes)
	}
}

// testOrders stands in for Order Service, answering for registered orders.
type testOrders map[uuid.UUID]orders.Participants

func (o testOrders) GetOrderParticipants(_ context.Context, id uuid.UUID) (orders.Participants, error) {
	participants, ok := o[id]
	if !ok {
		return orders.Participants{}, orders.ErrNotFound
	}
	return participants, nil
}

// order registers an order between requester and courier.
func (it integration) order(requester, courier uuid.UUID) uuid.UUID {
	id := uuid.New()
	it.orders[id] = orders.Participants{RequesterID: requester, CourierID: courier, Completed: true}
	return id
}
