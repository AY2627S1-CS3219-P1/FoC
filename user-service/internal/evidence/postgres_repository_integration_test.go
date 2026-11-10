package evidence

import (
	"context"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/evidencedb"
	"github.com/google/uuid"
)

func TestEvidenceIntegration(t *testing.T) {
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
	ctx := context.Background()

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := sqlDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	reporter, subject := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{reporter, subject} {
		exec("INSERT INTO users (id, email, display_name, role) VALUES ($1, $2, 'U', 'user')", id, id.String()+"@x.com")
	}
	reportID, responseID := uuid.New(), uuid.New()
	exec(`INSERT INTO user_reports (id, reporter_id, subject_id, order_id, category, description)
	      VALUES ($1, $2, $3, $4, 'other', 'details')`, reportID, reporter, subject, uuid.New())
	exec(`INSERT INTO user_report_responses (id, report_id, responder_id, body) VALUES ($1, $2, $3, 'reply')`,
		responseID, reportID, subject)

	storage := &fakeStorage{objects: map[string]images.ObjectInfo{}}
	service := NewService(NewPostgresRepository(evidencedb.New(sqlDB)), storage)
	upload := func(owner uuid.UUID) uuid.UUID {
		t.Helper()
		id, _, err := service.CreateUpload(ctx, owner, "image/png", 64)
		if err != nil {
			t.Fatal(err)
		}
		storage.objects["evidence/"+id.String()] = images.ObjectInfo{Exists: true, Size: 64, ContentType: "image/png"}
		return id
	}
	attach := func(owner uuid.UUID, parent Parent, ids ...uuid.UUID) error {
		t.Helper()
		prepared, err := service.Prepare(ctx, owner, ids)
		if err != nil {
			return err
		}
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := NewAttacher(tx).Attach(ctx, owner, parent, prepared); err != nil {
			return err
		}
		return tx.Commit()
	}

	// Attach in order to a report and to a response.
	first, second := upload(reporter), upload(reporter)
	if err := attach(reporter, ReportParent(reportID), second, first); err != nil {
		t.Fatal(err)
	}
	reply := upload(subject)
	if err := attach(subject, ReportResponseParent(responseID), reply); err != nil {
		t.Fatal(err)
	}
	views, err := service.Views(ctx, []Parent{ReportParent(reportID), ReportResponseParent(responseID)})
	if err != nil {
		t.Fatal(err)
	}
	report := views[ReportParent(reportID)]
	if len(report) != 2 || report[0].ID != second || report[1].ID != first ||
		len(views[ReportResponseParent(responseID)]) != 1 {
		t.Fatalf("views = %+v", views)
	}

	// A response image is governed by its report.
	owner, err := service.Locate(ctx, reply)
	if err != nil || owner.ReportID != reportID || owner.AppealID != uuid.Nil {
		t.Fatalf("locate = %+v, %v", owner, err)
	}

	// An attached image cannot be attached again, and a stale preparation
	// rolls its transaction back.
	if err := attach(reporter, ReportParent(reportID), first); err == nil {
		t.Fatal("expected an attached image to be rejected")
	}
	stale := upload(reporter)
	prepared, err := service.Prepare(ctx, reporter, []uuid.UUID{stale})
	if err != nil {
		t.Fatal(err)
	}
	prepared[0].SizeBytes++
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewAttacher(tx).Attach(ctx, reporter, ReportParent(reportID), prepared); err == nil {
		t.Fatal("expected a changed image to fail the attach")
	}
	_ = tx.Rollback()

	// Cleanup reaps the stale staged image once it has expired.
	exec(`UPDATE evidence_attachments SET created_at = now() - interval '2 days',
	      upload_expires_at = now() - interval '2 days' WHERE id = $1`, stale)
	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM evidence_attachments WHERE id = $1", stale).
		Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || len(storage.deleted) == 0 ||
		storage.deleted[len(storage.deleted)-1] != "evidence/"+stale.String() {
		t.Fatalf("remaining = %d, deleted objects = %v", remaining, storage.deleted)
	}
}
