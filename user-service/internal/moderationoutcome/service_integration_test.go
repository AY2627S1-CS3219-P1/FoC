package moderationoutcome

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
)

func openDatabase(t *testing.T) (*gorm.DB, *sql.DB) {
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
	return db, sqlDB
}

func insertUser(t *testing.T, sqlDB *sql.DB, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := sqlDB.Exec(
		"INSERT INTO users (id, email, display_name, role) VALUES ($1, $2, $3, $4)",
		id, id.String()+"@example.com", "User "+id.String()[:8], role,
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func roleOf(t *testing.T, sqlDB *sql.DB, id uuid.UUID) string {
	t.Helper()
	var role string
	if err := sqlDB.QueryRow("SELECT role FROM users WHERE id = $1", id).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

func mustApply(t *testing.T, s *Service, o Outcome, want Result) {
	t.Helper()
	got, err := s.Apply(context.Background(), o)
	if err != nil || got != want {
		t.Fatalf("apply %s = %q, %v; want %q", o.Action, got, err, want)
	}
}

func TestOutcomesApplyOnceAgainstPostgreSQL(t *testing.T) {
	db, sqlDB := openDatabase(t)
	s := &Service{DB: db}
	admin := insertUser(t, sqlDB, "admin")
	subject := insertUser(t, sqlDB, "user")
	reportID, appealID, requestID := uuid.New(), uuid.New(), uuid.New()

	// A report's warning, delivered twice, is recorded once.
	warn := Outcome{
		EventID: uuid.New(), Action: ActionWarnUser, ActorID: admin, Reason: "rude",
		ReportID: reportID, UserID: subject, RequestID: requestID,
	}
	mustApply(t, s, warn, ResultApplied)
	mustApply(t, s, warn, ResultDuplicate)
	var active int
	if err := sqlDB.QueryRow(
		"SELECT count(*) FROM account_warnings WHERE report_id = $1 AND status = 'active'", reportID,
	).Scan(&active); err != nil || active != 1 {
		t.Fatalf("report warnings = %d, %v; want 1", active, err)
	}

	// An appeal overturns it through the report, linking the appeal.
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionRemoveWarning, ActorID: admin, Reason: "overturned",
		ReportID: reportID, AppealID: appealID,
	}, ResultApplied)
	var linked uuid.NullUUID
	if err := sqlDB.QueryRow(
		"SELECT appeal_id FROM account_warnings WHERE report_id = $1 AND status = 'removed'", reportID,
	).Scan(&linked); err != nil || linked.UUID != appealID {
		t.Fatalf("removed warning appeal = %v, %v", linked, err)
	}

	// Removing it again is rejected, recorded and not retried.
	again := Outcome{
		EventID: uuid.New(), Action: ActionRemoveWarning, ActorID: admin, Reason: "again",
		ReportID: reportID, AppealID: appealID,
	}
	mustApply(t, s, again, ResultRejected)
	mustApply(t, s, again, ResultDuplicate)

	// A suspension and its reinstatement return the user to their earlier role.
	suspensionReport := uuid.New()
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionSuspendUser, ActorID: admin, Reason: "fraud",
		ReportID: suspensionReport, UserID: subject,
	}, ResultApplied)
	if role := roleOf(t, sqlDB, subject); role != "suspended" {
		t.Fatalf("role after suspension = %q", role)
	}
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionReinstateUser, ActorID: admin, Reason: "overturned",
		ReportID: suspensionReport, AppealID: appealID, UserID: subject,
	}, ResultApplied)
	if role := roleOf(t, sqlDB, subject); role != "user" {
		t.Fatalf("role after reinstatement = %q", role)
	}

	// A report cannot suspend an administrator; the event is rejected.
	otherAdmin := insertUser(t, sqlDB, "admin")
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionSuspendUser, ActorID: admin, Reason: "fraud",
		ReportID: uuid.New(), UserID: otherAdmin,
	}, ResultRejected)
	if role := roleOf(t, sqlDB, otherAdmin); role != "admin" {
		t.Fatalf("administrator role changed to %q", role)
	}

	// A report removes a review, and an appeal restores it.
	reviewID := uuid.New()
	if _, err := sqlDB.Exec(
		`INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at)
		 VALUES ($1, $2, $3, $4, 1, 'bad', now())`,
		reviewID, subject, admin, uuid.New(),
	); err != nil {
		t.Fatal(err)
	}
	reviewReport := uuid.New()
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionRemoveReview, ActorID: admin, Reason: "abusive",
		ReportID: reviewReport, ReviewID: reviewID,
	}, ResultApplied)
	var removalReport uuid.NullUUID
	if err := sqlDB.QueryRow(
		"SELECT report_id FROM review_removals WHERE review_id = $1", reviewID,
	).Scan(&removalReport); err != nil || removalReport.UUID != reviewReport {
		t.Fatalf("removal report = %v, %v", removalReport, err)
	}
	mustApply(t, s, Outcome{
		EventID: uuid.New(), Action: ActionRestoreReview, ActorID: admin, Reason: "overturned",
		ReportID: reviewReport, AppealID: appealID, ReviewID: reviewID,
	}, ResultApplied)
	var removed bool
	if err := sqlDB.QueryRow(
		"SELECT deleted_at IS NOT NULL FROM reviews WHERE id = $1", reviewID,
	).Scan(&removed); err != nil || removed {
		t.Fatalf("review still removed: %v, %v", removed, err)
	}
}
