package appeal_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/reportoutcome"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/google/uuid"
)

type integration struct {
	t       *testing.T
	ctx     context.Context
	sqlDB   *sql.DB
	appeals *appeal.Service
	reports *report.Service
	reviews *review.Service
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

	// The services are wired as the composition root wires them.
	reviewQueries := reviewdb.New(sqlDB)
	it := integration{
		t:     t,
		ctx:   context.Background(),
		sqlDB: sqlDB,
		appeals: appeal.NewService(
			appeal.NewPostgresRepository(appealdb.New(sqlDB)), reportoutcome.WithAppealTransaction(db)),
		reports: report.NewService(
			report.NewPostgresRepository(reportdb.New(sqlDB)),
			reportoutcome.WithTransaction(db),
			evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(sqlDB)), nil),
		),
		reviews: review.NewService(
			review.NewPostgresRepository(reviewQueries),
			func(_ context.Context, operation func(*reviewdb.Queries) error) error {
				return operation(reviewQueries)
			},
			nil,
		),
	}
	it.admin = it.user("Admin", "admin")
	return it
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

func (it integration) review(reviewer, reviewee uuid.UUID, rating int) uuid.UUID {
	it.t.Helper()

	id := uuid.New()
	if _, err := it.sqlDB.ExecContext(it.ctx,
		`INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment)
		 VALUES ($1, $2, $3, $4, $5, 'review')`,
		id, reviewer, reviewee, uuid.New(), rating); err != nil {
		it.t.Fatalf("insert review: %v", err)
	}
	return id
}

// closedReport files a report and closes it with outcome.
func (it integration) closedReport(reporter uuid.UUID, target report.Target, outcome report.Outcome) uuid.UUID {
	it.t.Helper()

	filed, err := it.reports.SubmitReport(it.ctx, reporter, report.Submission{
		Target:      target,
		Category:    report.CategoryHarassment,
		Description: "inappropriate behaviour",
	})
	if err != nil {
		it.t.Fatalf("submit report: %v", err)
	}
	if _, err := it.reports.CloseReport(it.ctx, it.admin, filed.ID, outcome, "decided"); err != nil {
		it.t.Fatalf("close report: %v", err)
	}
	return filed.ID
}

func (it integration) submit(appellant uuid.UUID, targets ...appeal.Target) appeal.Appeal {
	it.t.Helper()

	got, err := it.appeals.SubmitAppeal(it.ctx, appellant, appeal.Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "this decision was wrong",
		Targets:        targets,
	})
	if err != nil {
		it.t.Fatalf("submit appeal: %v", err)
	}
	return got
}

// close resolves every decision of the appeal with outcome.
func (it integration) close(got appeal.Appeal, outcome appeal.Outcome) (appeal.Appeal, error) {
	it.t.Helper()

	resolutions := make([]appeal.Resolution, len(got.Decisions))
	for i, decision := range got.Decisions {
		resolutions[i] = appeal.Resolution{DecisionID: decision.ID, Outcome: outcome, Reason: "resolved"}
	}
	return it.appeals.CloseAppeal(it.ctx, it.admin, got.ID, resolutions)
}

func (it integration) scalar(query string, args ...any) string {
	it.t.Helper()

	var value sql.NullString
	if err := it.sqlDB.QueryRowContext(it.ctx, query, args...).Scan(&value); err != nil {
		it.t.Fatal(err)
	}
	return value.String
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()

	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != status {
		t.Fatalf("error = %v, want HTTP status %d", err, status)
	}
}

func TestAppealLifecycleIntegration(t *testing.T) {
	it := setupIntegration(t)
	requester, courier := it.user("Requester", "user"), it.user("Courier", "user")
	orderID := uuid.New()
	reportID := it.closedReport(requester, report.OrderTarget(orderID, courier), report.OutcomeWarning)
	warningID := it.scalar("SELECT id FROM account_warnings WHERE report_id = $1", reportID)

	// Submission, idempotency and duplicate appeals (RP7)

	submission := appeal.Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "I was on time",
		Targets:        []appeal.Target{{Kind: appeal.KindReportOutcome, TargetID: reportID}},
	}
	submitted, err := it.appeals.SubmitAppeal(it.ctx, courier, submission)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.RequestID != orderID || submitted.AppellantName != "Courier" ||
		submitted.Decisions[0].CounterpartName != "Requester" {
		t.Fatalf("submitted = %+v", submitted)
	}
	again, err := it.appeals.SubmitAppeal(it.ctx, courier, submission)
	if err != nil || again.ID != submitted.ID {
		t.Fatalf("resubmission = %v, %v; want %v", again.ID, err, submitted.ID)
	}
	if n := it.scalar("SELECT count(*) FROM user_appeals WHERE appellant_id = $1", courier); n != "1" {
		t.Fatalf("appeals = %s, want 1", n)
	}
	_, err = it.appeals.SubmitAppeal(it.ctx, courier, appeal.Submission{
		IdempotencyKey: uuid.New(),
		Reason:         "again",
		Targets:        submission.Targets,
	})
	wantStatus(t, err, http.StatusConflict)

	// Visibility (RP11.3)

	firstPage, err := pagination.New(1, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range []uuid.UUID{courier, requester} {
		page, err := it.appeals.ListMyAppeals(it.ctx, participant, firstPage)
		if err != nil || len(page.Appeals) != 1 || page.TotalItems() != 1 {
			t.Fatalf("list for participant: %+v, %v", page, err)
		}
	}
	_, err = it.appeals.GetAppeal(it.ctx, it.user("Stranger", "user"), false, submitted.ID)
	wantStatus(t, err, http.StatusNotFound)

	// Response from the other participant (RP9)

	if _, err := it.appeals.BeginReview(it.ctx, it.admin, submitted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := it.appeals.RequestResponse(it.ctx, it.admin, submitted.ID, requester); err != nil {
		t.Fatal(err)
	}
	responded, response, err := it.appeals.SubmitResponse(it.ctx, requester, submitted.ID, "they were late")
	if err != nil || responded.Status != appeal.StatusUnderReview || response.ResponderName != "Requester" {
		t.Fatalf("respond: %+v %+v %v", responded, response, err)
	}

	// Closing applies the overturned outcome (RP11, RP12.1)

	closed, err := it.close(submitted, appeal.OutcomeOverturned)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != appeal.StatusClosed || closed.CloserName != "Admin" ||
		closed.Decisions[0].Outcome != appeal.OutcomeOverturned {
		t.Fatalf("closed = %+v", closed)
	}
	if status := it.scalar("SELECT status FROM account_warnings WHERE id = $1", warningID); status != "removed" {
		t.Fatalf("warning status = %s, want removed", status)
	}
	if linked := it.scalar(
		"SELECT appeal_id FROM account_warnings WHERE id = $1",
		warningID,
	); linked != submitted.ID.String() {
		t.Fatalf("warning appeal_id = %s, want %s", linked, submitted.ID)
	}

	// A closed decision may be appealed again; an already removed warning
	// cannot be removed twice, so overturning it rolls the close back.
	second := it.submit(courier, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
	_, err = it.close(second, appeal.OutcomeOverturned)
	wantStatus(t, err, http.StatusPreconditionFailed)
	if status := it.scalar("SELECT status FROM user_appeals WHERE id = $1", second.ID); status != "open" {
		t.Fatalf("appeal status after a failed close = %s, want open", status)
	}
	if active := it.scalar(
		"SELECT active::TEXT FROM user_appeal_decisions WHERE appeal_id = $1",
		second.ID,
	); active != "true" {
		t.Fatal("decision resolved by a rolled-back close")
	}
	if upheld, err := it.close(second, appeal.OutcomeUpheld); err != nil || upheld.Status != appeal.StatusClosed {
		t.Fatalf("uphold: %+v, %v", upheld, err)
	}
}

func TestAppealOutcomesIntegration(t *testing.T) {
	it := setupIntegration(t)

	t.Run("overturned suspension reinstates the user", func(t *testing.T) {
		requester, courier := it.user("Requester", "user"), it.user("Courier", "user")
		reportID := it.closedReport(requester, report.OrderTarget(uuid.New(), courier), report.OutcomeSuspension)
		if role := it.scalar("SELECT role FROM users WHERE id = $1", courier); role != "suspended" {
			t.Fatalf("role = %s, want suspended", role)
		}

		// A suspended user may appeal (U6.8.1).
		got := it.submit(courier, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if role := it.scalar("SELECT role FROM users WHERE id = $1", courier); role != "user" {
			t.Fatalf("role = %s, want user", role)
		}
		if n := it.scalar(
			"SELECT count(*) FROM role_changes WHERE user_id = $1 AND report_id = $2", courier, reportID,
		); n != "2" {
			t.Fatalf("role changes linked to the report = %s, want suspension and reinstatement", n)
		}
	})

	t.Run("overturned report review removal restores the review", func(t *testing.T) {
		author, reviewee := it.user("Author", "user"), it.user("Reviewee", "user")
		reviewID := it.review(author, reviewee, 3)
		reportID := it.closedReport(reviewee, report.ReviewTarget(reviewID), report.OutcomeReviewRemoval)

		_, err := it.appeals.SubmitAppeal(it.ctx, author, appeal.Submission{
			IdempotencyKey: uuid.New(),
			Reason:         "fair review",
			Targets:        []appeal.Target{{Kind: appeal.KindReviewRemoval, TargetID: reviewID}},
		})
		wantStatus(t, err, http.StatusPreconditionFailed)

		got := it.submit(author, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if deleted := it.scalar("SELECT deleted_at::TEXT FROM reviews WHERE id = $1", reviewID); deleted != "" {
			t.Fatalf("review still removed at %s", deleted)
		}
		if count := it.scalar("SELECT review_count FROM users WHERE id = $1", reviewee); count != "1" {
			t.Fatalf("review_count = %s, want 1", count)
		}
		if linked := it.scalar(
			"SELECT appeal_id FROM review_restorations WHERE review_id = $1",
			reviewID,
		); linked != got.ID.String() {
			t.Fatalf("restoration appeal_id = %s, want %s", linked, got.ID)
		}
	})

	t.Run("overturned review retention removes the review", func(t *testing.T) {
		author, reviewee := it.user("Author", "user"), it.user("Reviewee", "user")
		reviewID := it.review(author, reviewee, 1)
		it.closedReport(reviewee, report.ReviewTarget(reviewID), report.OutcomeNoAction)

		got := it.submit(reviewee, appeal.Target{Kind: appeal.KindReviewRetention, TargetID: reviewID})
		if got.Decisions[0].CounterpartID != author {
			t.Fatalf("counterpart = %v, want the author", got.Decisions[0].CounterpartID)
		}
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if deleted := it.scalar("SELECT deleted_at::TEXT FROM reviews WHERE id = $1", reviewID); deleted == "" {
			t.Fatal("retained review was not removed")
		}
	})

	t.Run("overturned administrative removal restores the review", func(t *testing.T) {
		author, reviewee := it.user("Author", "user"), it.user("Reviewee", "user")
		reviewID := it.review(author, reviewee, 4)
		if err := it.reviews.RemoveReview(it.ctx, reviewID, "spam", it.admin); err != nil {
			t.Fatal(err)
		}

		got := it.submit(author, appeal.Target{Kind: appeal.KindReviewRemoval, TargetID: reviewID})
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if deleted := it.scalar("SELECT deleted_at::TEXT FROM reviews WHERE id = $1", reviewID); deleted != "" {
			t.Fatalf("review still removed at %s", deleted)
		}
	})

	t.Run("upheld warning stays active", func(t *testing.T) {
		courier := it.user("Courier", "user")
		warningID := uuid.New()
		if _, err := it.sqlDB.ExecContext(it.ctx,
			"INSERT INTO account_warnings (id, user_id, request_id, reason) VALUES ($1, $2, $3, 'late')",
			warningID, courier, uuid.New()); err != nil {
			t.Fatal(err)
		}

		got := it.submit(courier, appeal.Target{Kind: appeal.KindWarning, TargetID: warningID})
		closed, err := it.close(got, appeal.OutcomeUpheld)
		if err != nil || closed.Decisions[0].Outcome != appeal.OutcomeUpheld {
			t.Fatalf("close: %+v, %v", closed, err)
		}
		if status := it.scalar("SELECT status FROM account_warnings WHERE id = $1", warningID); status != "active" {
			t.Fatalf("warning status = %s, want active", status)
		}
	})

	t.Run("no-action report outcome records the disposition only", func(t *testing.T) {
		requester, courier := it.user("Requester", "user"), it.user("Courier", "user")
		reportID := it.closedReport(requester, report.OrderTarget(uuid.New(), courier), report.OutcomeNoAction)

		got := it.submit(requester, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
		closed, err := it.close(got, appeal.OutcomeOverturned)
		if err != nil || closed.Decisions[0].Outcome != appeal.OutcomeOverturned {
			t.Fatalf("close: %+v, %v", closed, err)
		}
	})
}
