package report_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/reportoutcome"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type integration struct {
	t       *testing.T
	ctx     context.Context
	sqlDB   *sql.DB
	service *report.Service
	admin   uuid.UUID
}

func setupIntegration(t *testing.T) integration {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}

	it := integration{
		t:       t,
		ctx:     context.Background(),
		sqlDB:   sqlDB,
		service: newService(db, sqlDB),
	}
	it.admin = it.user("Admin", "admin")
	return it
}

// newService wires the report service exactly as the composition root does.
func newService(db *gorm.DB, sqlDB *sql.DB) *report.Service {
	return report.NewService(
		report.NewPostgresRepository(reportdb.New(sqlDB)),
		reportoutcome.WithTransaction(db),
		evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(sqlDB)), nil),
	)
}

func (it integration) user(name, role string) uuid.UUID {
	it.t.Helper()

	id := uuid.New()
	if _, err := it.sqlDB.ExecContext(it.ctx,
		"INSERT INTO users (id, email, display_name, role) VALUES ($1, $2, $3, $4)",
		id, id.String()+"@example.com", name, role); err != nil {
		it.t.Fatalf("insert user %s: %v", name, err)
	}
	return id
}

func (it integration) review(reviewer, reviewee, order uuid.UUID) uuid.UUID {
	it.t.Helper()

	id := uuid.New()
	if _, err := it.sqlDB.ExecContext(it.ctx,
		`INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment)
		 VALUES ($1, $2, $3, $4, 1, 'rude')`,
		id, reviewer, reviewee, order); err != nil {
		it.t.Fatalf("insert review: %v", err)
	}
	return id
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
	if err := it.sqlDB.QueryRowContext(it.ctx, query, args...).Scan(&n); err != nil {
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

	requester := it.user("Requester", "user")
	courier := it.user("Courier", "user")
	orderID := uuid.New()

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

	// Warning outcome (RP3.5, RP5.2)

	closed, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeWarning, "late twice")
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != report.StatusClosed || closed.Outcome != report.OutcomeWarning ||
		closed.OutcomeReason != "late twice" || closed.CloserName != "Admin" || closed.ClosedAt.IsZero() {
		t.Fatalf("closed = %+v", closed)
	}
	if n := it.count(
		"SELECT count(*) FROM account_warnings WHERE user_id = $1 AND request_id = $2 AND report_id = $3 AND reason = $4",
		courier,
		orderID,
		submitted.ID,
		"late twice",
	); n != 1 {
		t.Fatalf("warnings = %d, want 1", n)
	}

	_, _, err = it.service.SubmitResponse(it.ctx, courier, submitted.ID, "too late", nil)
	wantStatus(t, err, http.StatusPreconditionFailed)

	// A closed report frees the target for a new one.
	it.submit(requester, report.OrderTarget(orderID, courier))
}

func TestReportSuspensionOutcomeIntegration(t *testing.T) {
	it := setupIntegration(t)

	reporter := it.user("Reporter", "user")
	subject := it.user("Subject", "user")
	submitted := it.submit(reporter, report.OrderTarget(uuid.New(), subject))

	if _, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeSuspension, "fraud"); err != nil {
		t.Fatal(err)
	}
	if n := it.count("SELECT count(*) FROM users WHERE id = $1 AND role = 'suspended'", subject); n != 1 {
		t.Fatal("subject was not suspended")
	}
	if n := it.count(
		"SELECT count(*) FROM role_changes WHERE user_id = $1 AND to_role = 'suspended' AND report_id = $2 AND created_by = $3",
		subject,
		submitted.ID,
		it.admin,
	); n != 1 {
		t.Fatalf("role changes = %d, want 1", n)
	}

	// A consequence the role rules reject rolls back the whole close.
	other := it.submit(reporter, report.OrderTarget(uuid.New(), subject))
	_, err := it.service.CloseReport(it.ctx, it.admin, other.ID, report.OutcomeSuspension, "again")
	wantStatus(t, err, http.StatusPreconditionFailed)
	if n := it.count("SELECT count(*) FROM user_reports WHERE id = $1 AND status = 'open'", other.ID); n != 1 {
		t.Fatal("rejected suspension still closed the report")
	}
}

func TestReportReviewRemovalOutcomeIntegration(t *testing.T) {
	it := setupIntegration(t)

	reviewee := it.user("Reviewee", "user")
	author := it.user("Author", "user")
	orderID := uuid.New()
	reviewID := it.review(author, reviewee, orderID)
	submitted := it.submit(reviewee, report.ReviewTarget(reviewID))
	if submitted.SubjectID != author {
		t.Fatalf("subject = %s, want review author", submitted.SubjectID)
	}

	if _, err := it.service.CloseReport(
		it.ctx, it.admin, submitted.ID, report.OutcomeReviewRemoval, "abusive language",
	); err != nil {
		t.Fatal(err)
	}
	if n := it.count("SELECT count(*) FROM reviews WHERE id = $1 AND deleted_at IS NOT NULL", reviewID); n != 1 {
		t.Fatal("review was not removed")
	}
	if n := it.count(
		"SELECT count(*) FROM review_removals WHERE review_id = $1 AND created_by = $2", reviewID, it.admin,
	); n != 1 {
		t.Fatal("review removal was not audited")
	}

	// A removed review can no longer be newly reported.
	_, err := it.service.SubmitReport(it.ctx, it.user("Other", "user"), report.Submission{
		Target:      report.ReviewTarget(reviewID),
		Category:    report.CategoryOther,
		Description: "late report",
	})
	wantStatus(t, err, http.StatusNotFound)
}

func TestReportWarningUsesReviewOrderIntegration(t *testing.T) {
	it := setupIntegration(t)

	reviewee := it.user("Reviewee", "user")
	author := it.user("Author", "user")
	orderID := uuid.New()
	submitted := it.submit(reviewee, report.ReviewTarget(it.review(author, reviewee, orderID)))

	if _, err := it.service.CloseReport(it.ctx, it.admin, submitted.ID, report.OutcomeWarning, "rude"); err != nil {
		t.Fatal(err)
	}
	if n := it.count(
		"SELECT count(*) FROM account_warnings WHERE user_id = $1 AND request_id = $2 AND report_id = $3",
		author, orderID, submitted.ID,
	); n != 1 {
		t.Fatal("warning did not record the review's order")
	}
}
