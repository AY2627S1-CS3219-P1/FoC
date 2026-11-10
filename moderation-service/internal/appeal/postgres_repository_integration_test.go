package appeal_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/orders"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/testsupport"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
)

type integration struct {
	t         *testing.T
	ctx       context.Context
	db        *sql.DB
	appeals   *appeal.Service
	reports   *report.Service
	orders    testOrders
	directory *testsupport.Directory
	admin     uuid.UUID
}

func setupIntegration(t *testing.T) integration {
	t.Helper()
	db := testsupport.Database(t)
	directory := testsupport.NewDirectory()
	images := evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(db)), nil)
	known := testOrders{}

	// The services are wired as the composition root wires them, with an
	// in-memory User Service.
	return integration{
		t:   t,
		ctx: context.Background(),
		db:  db,
		appeals: appeal.NewService(
			appeal.NewPostgresRepository(appealdb.New(db)),
			outbox.AppealTransaction(db),
			images,
			directory,
		),
		reports: report.NewService(
			report.NewPostgresRepository(reportdb.New(db)),
			outbox.ReportTransaction(db),
			images,
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

// overturned returns the single outcome the appeal recorded.
func (it integration) overturned(t *testing.T, appealID uuid.UUID) *eventsv1.OutcomeRequested {
	t.Helper()
	outcomes := testsupport.Outcomes(t, it.db, uuid.Nil, appealID)
	if len(outcomes) != 1 {
		t.Fatalf("appeal outcomes = %v, want one", outcomes)
	}
	return outcomes[0]
}

func (it integration) scalar(query string, args ...any) string {
	it.t.Helper()
	var value sql.NullString
	if err := it.db.QueryRowContext(it.ctx, query, args...).Scan(&value); err != nil {
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
	requester, courier := it.user("Requester"), it.user("Courier")
	orderID := it.order(requester, courier)
	reportID := it.closedReport(requester, report.OrderTarget(orderID, courier), report.OutcomeWarning)

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
	if n := it.scalar("SELECT count(*) FROM appeals WHERE appellant_id = $1", courier); n != "1" {
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
	_, err = it.appeals.GetAppeal(it.ctx, it.user("Stranger"), false, submitted.ID)
	wantStatus(t, err, http.StatusNotFound)

	// Response from the other participant (RP9)

	if _, err := it.appeals.BeginReview(it.ctx, it.admin, submitted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := it.appeals.RequestResponse(it.ctx, it.admin, submitted.ID, requester); err != nil {
		t.Fatal(err)
	}
	responded, response, err := it.appeals.SubmitResponse(it.ctx, requester, submitted.ID, "they were late", nil)
	if err != nil || responded.Status != appeal.StatusUnderReview || response.ResponderName != "Requester" {
		t.Fatalf("respond: %+v %+v %v", responded, response, err)
	}

	// Closing records the undo for User Service (RP11, RP12.1)

	closed, err := it.close(submitted, appeal.OutcomeOverturned)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != appeal.StatusClosed || closed.CloserName != "Admin" ||
		closed.Decisions[0].Outcome != appeal.OutcomeOverturned {
		t.Fatalf("closed = %+v", closed)
	}
	removal := it.overturned(t, submitted.ID)
	if removal.GetRemoveWarning().GetByReportId() != reportID.String() || removal.GetReportId() != reportID.String() {
		t.Fatalf("overturn outcome = %v", removal)
	}

	// A closed decision may be appealed again. Whether the warning is still
	// there is User Service's to decide when it applies the outcome.
	second := it.submit(courier, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
	if upheld, err := it.close(second, appeal.OutcomeUpheld); err != nil || upheld.Status != appeal.StatusClosed {
		t.Fatalf("uphold: %+v, %v", upheld, err)
	}
	if outcomes := testsupport.Outcomes(t, it.db, uuid.Nil, second.ID); len(outcomes) != 0 {
		t.Fatalf("an upheld decision recorded outcomes: %v", outcomes)
	}
}

func TestAppealOutcomesIntegration(t *testing.T) {
	it := setupIntegration(t)

	t.Run("overturned suspension reinstates the user", func(t *testing.T) {
		requester, courier := it.user("Requester"), it.user("Courier")
		reportID := it.closedReport(
			requester,
			report.OrderTarget(it.order(requester, courier), courier),
			report.OutcomeSuspension,
		)

		// A suspended user may appeal (U6.8.1).
		got := it.submit(courier, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		outcome := it.overturned(t, got.ID)
		if outcome.GetReinstateUser().GetUserId() != courier.String() || outcome.GetReportId() != reportID.String() {
			t.Fatalf("overturn outcome = %v", outcome)
		}
	})

	t.Run("overturned report review removal restores the review", func(t *testing.T) {
		author, reviewee := it.user("Author"), it.user("Reviewee")
		reviewID := it.directory.Review(author, reviewee, uuid.New())
		reportID := it.closedReport(reviewee, report.ReviewTarget(reviewID), report.OutcomeReviewRemoval)
		it.directory.RemoveReview(reviewID, reportID)

		// A removal a report made is appealed through the report.
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
		outcome := it.overturned(t, got.ID)
		if outcome.GetRestoreReview().GetReviewId() != reviewID.String() {
			t.Fatalf("overturn outcome = %v", outcome)
		}
	})

	t.Run("overturned review retention removes the review", func(t *testing.T) {
		author, reviewee := it.user("Author"), it.user("Reviewee")
		reviewID := it.directory.Review(author, reviewee, uuid.New())
		it.closedReport(reviewee, report.ReviewTarget(reviewID), report.OutcomeNoAction)

		got := it.submit(reviewee, appeal.Target{Kind: appeal.KindReviewRetention, TargetID: reviewID})
		if got.Decisions[0].CounterpartID != author {
			t.Fatalf("counterpart = %v, want the author", got.Decisions[0].CounterpartID)
		}
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if outcome := it.overturned(t, got.ID); outcome.GetRemoveReview().GetReviewId() != reviewID.String() {
			t.Fatalf("overturn outcome = %v", outcome)
		}
	})

	t.Run("overturned administrative removal restores the review", func(t *testing.T) {
		author, reviewee := it.user("Author"), it.user("Reviewee")
		reviewID := it.directory.Review(author, reviewee, uuid.New())
		it.directory.RemoveReview(reviewID, uuid.Nil)

		got := it.submit(author, appeal.Target{Kind: appeal.KindReviewRemoval, TargetID: reviewID})
		if _, err := it.close(got, appeal.OutcomeOverturned); err != nil {
			t.Fatal(err)
		}
		if outcome := it.overturned(t, got.ID); outcome.GetRestoreReview().GetReviewId() != reviewID.String() {
			t.Fatalf("overturn outcome = %v", outcome)
		}
	})

	t.Run("upheld warning records nothing", func(t *testing.T) {
		courier := it.user("Courier")
		warningID := it.directory.Warning(courier, uuid.New(), uuid.Nil)

		got := it.submit(courier, appeal.Target{Kind: appeal.KindWarning, TargetID: warningID})
		closed, err := it.close(got, appeal.OutcomeUpheld)
		if err != nil || closed.Decisions[0].Outcome != appeal.OutcomeUpheld {
			t.Fatalf("close: %+v, %v", closed, err)
		}
		if outcomes := testsupport.Outcomes(t, it.db, uuid.Nil, got.ID); len(outcomes) != 0 {
			t.Fatalf("outcomes = %v", outcomes)
		}
	})

	t.Run("a warning a report issued is appealed through the report", func(t *testing.T) {
		courier := it.user("Courier")
		warningID := it.directory.Warning(courier, uuid.New(), uuid.New())
		_, err := it.appeals.SubmitAppeal(it.ctx, courier, appeal.Submission{
			IdempotencyKey: uuid.New(),
			Reason:         "unfair",
			Targets:        []appeal.Target{{Kind: appeal.KindWarning, TargetID: warningID}},
		})
		wantStatus(t, err, http.StatusPreconditionFailed)
	})

	t.Run("no-action report outcome records the disposition only", func(t *testing.T) {
		requester, courier := it.user("Requester"), it.user("Courier")
		reportID := it.closedReport(
			requester,
			report.OrderTarget(it.order(requester, courier), courier),
			report.OutcomeNoAction,
		)

		got := it.submit(requester, appeal.Target{Kind: appeal.KindReportOutcome, TargetID: reportID})
		closed, err := it.close(got, appeal.OutcomeOverturned)
		if err != nil || closed.Decisions[0].Outcome != appeal.OutcomeOverturned {
			t.Fatalf("close: %+v, %v", closed, err)
		}
		if outcomes := testsupport.Outcomes(t, it.db, uuid.Nil, got.ID); len(outcomes) != 0 {
			t.Fatalf("outcomes = %v", outcomes)
		}
	})
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
