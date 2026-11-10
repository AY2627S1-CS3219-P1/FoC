package warning

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func setupWarningRepository(t *testing.T) (*PostgresRepository, *gorm.DB, models.User, models.User) {
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
	if err := db.Exec("TRUNCATE users CASCADE").Error; err != nil {
		t.Fatal(err)
	}

	actor := models.User{Email: "warning-admin@example.com", DisplayName: "Admin", Role: models.RoleAdmin}
	user := models.User{Email: "warning-user@example.com", DisplayName: "User", Role: models.RoleUser}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(warningdb.New(sqlDB))
	return repository, db, actor, user
}

func TestPostgresRepositoryCreateListAndRemove(t *testing.T) {
	repository, _, actor, user := setupWarningRepository(t)
	ctx := context.Background()
	eventID, requestID := uuid.New(), uuid.New()
	createdBy := actor.ID
	beforeCreate := time.Now().UTC().Truncate(time.Microsecond)
	created, err := repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: requestID, Reason: "late cancellation", Status: StatusActive,
		SourceEventID: &eventID, CreatedBy: &createdBy,
	})
	if err != nil || !created {
		t.Fatalf("create warning: created=%v err=%v", created, err)
	}

	created, err = repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: uuid.New(), Reason: "retry payload", Status: StatusActive,
		SourceEventID: &eventID,
	})
	if err != nil || created {
		t.Fatalf("duplicate event: created=%v err=%v", created, err)
	}
	warnings, err := repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 1 || warnings[0].RequestID != requestID || warnings[0].CreatedBy == nil ||
		*warnings[0].CreatedBy != actor.ID {
		t.Fatalf("listed warnings=%+v err=%v", warnings, err)
	}
	if warnings[0].CreatedAt.Before(beforeCreate) || warnings[0].CreatedAt.After(time.Now().UTC()) {
		t.Fatalf("default creation time = %s, want current database time", warnings[0].CreatedAt)
	}

	removedAt := time.Now().UTC()
	reason := "appeal overturned"
	updatedBy := actor.ID
	removed, err := repository.Remove(ctx, warnings[0].ID, removedAt, &reason, nil, &updatedBy)
	if err != nil || removed.Status != StatusRemoved || removed.RemovedAt == nil || removed.UpdatedBy == nil ||
		*removed.UpdatedBy != actor.ID {
		t.Fatalf("remove warning: %+v err=%v", removed, err)
	}
	if _, err := repository.Remove(
		ctx,
		warnings[0].ID,
		removedAt,
		&reason,
		nil,
		&updatedBy,
	); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("repeat removal: %v", err)
	}
	warnings, err = repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 1 || warnings[0].Status != StatusRemoved {
		t.Fatalf("removed warning missing from user history: %+v err=%v", warnings, err)
	}
}

func TestPostgresRepositoryListForAdminJoinsUpdaterName(t *testing.T) {
	repository, _, actor, user := setupWarningRepository(t)
	ctx := context.Background()
	updater := actor.ID
	// Equal timestamps force the id DESC tiebreaker to decide the order.
	listTime := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	eventID := uuid.New()
	if _, err := repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: uuid.New(), Reason: "admin recorded", Status: StatusActive,
		CreatedBy: &updater, UpdatedBy: &updater, CreatedAt: listTime,
	}); err != nil {
		t.Fatal(err)
	}
	// Courier event row: no updater, carries its source event ID.
	if _, err := repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: uuid.New(), Reason: "courier event", Status: StatusActive,
		SourceEventID: &eventID, CreatedAt: listTime,
	}); err != nil {
		t.Fatal(err)
	}

	warnings, err := repository.ListForAdmin(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 2 {
		t.Fatalf("admin warnings=%+v err=%v", warnings, err)
	}
	// Equal timestamps mean id DESC decides the order (Postgres uuid byte
	// order, matching bytes.Compare).
	if bytes.Compare(warnings[0].ID[:], warnings[1].ID[:]) < 0 {
		t.Fatalf("admin list not sorted by id DESC: [%s, %s]", warnings[0].ID, warnings[1].ID)
	}
	// Both rows must be present; which comes first is random (UUIDv4).
	var courier, recorded AdminWarning
	for _, warning := range warnings {
		switch warning.Reason {
		case "courier event":
			courier = warning
		case "admin recorded":
			recorded = warning
		default:
			t.Fatalf("unexpected admin list row: %+v", warning)
		}
	}
	if courier.Reason != "courier event" || recorded.Reason != "admin recorded" {
		t.Fatalf("admin list rows = [%s, %s]", warnings[0].Reason, warnings[1].Reason)
	}

	if courier.CreatedAt.Equal(listTime) == false || courier.UpdatedBy != nil || courier.UpdatedByName != nil ||
		courier.CreatedBy != nil || courier.SourceEventID == nil || *courier.SourceEventID != eventID ||
		courier.Status != StatusActive || courier.RemovedAt != nil || courier.RemovedReason != nil ||
		courier.ReportID != nil || courier.AppealID != nil || courier.DeletedAt != nil {
		t.Fatalf("system-created projection: %+v", courier)
	}
	if recorded.CreatedBy == nil || *recorded.CreatedBy != actor.ID ||
		recorded.UpdatedBy == nil || *recorded.UpdatedBy != actor.ID ||
		recorded.UpdatedByName == nil || *recorded.UpdatedByName != actor.DisplayName ||
		recorded.SourceEventID != nil || !recorded.CreatedAt.Equal(listTime) {
		t.Fatalf("admin-recorded projection: %+v", recorded)
	}
}

func TestPostgresRepositoryListForAdminShowsRemovedWarnings(t *testing.T) {
	repository, _, actor, user := setupWarningRepository(t)
	ctx := context.Background()
	warningID := uuid.New()
	if _, err := repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: warningID, Reason: "appealed warning", Status: StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed warnings=%+v err=%v", listed, err)
	}
	warningID = listed[0].ID

	removedAt := time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)
	removedReason, appealID := "appeal overturned", uuid.New()
	removed, err := repository.Remove(ctx, warningID, removedAt, &removedReason, &appealID, &actor.ID)
	if err != nil {
		t.Fatal(err)
	}

	warnings, err := repository.ListForAdmin(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("admin warnings=%+v err=%v", warnings, err)
	}
	got := warnings[0]
	if got.ID != removed.ID || got.Status != StatusRemoved || got.RemovedAt == nil ||
		!got.RemovedAt.Equal(removedAt) || got.RemovedReason == nil || *got.RemovedReason != removedReason ||
		got.AppealID == nil || *got.AppealID != appealID ||
		got.UpdatedBy == nil || *got.UpdatedBy != actor.ID ||
		got.UpdatedByName == nil || *got.UpdatedByName != actor.DisplayName ||
		got.DeletedAt != nil {
		t.Fatalf("removed warning audit view: %+v", got)
	}
}

func TestPostgresRepositoryCreateRejectsUnknownCourier(t *testing.T) {
	repository, _, _, _ := setupWarningRepository(t)
	ctx := context.Background()
	// The warned courier has no users row, so the insert violates
	// account_warnings.user_id -> users (23503). The consumer must be able to
	// recognise this as an unprocessable event rather than a transient failure.
	eventID := uuid.New()
	created, err := repository.Create(ctx, Warning{
		UserID: uuid.New(), RequestID: uuid.New(), Reason: "courier without an account",
		Status: StatusActive, SourceEventID: &eventID,
	})
	if created {
		t.Fatal("created a warning for a courier without a user account")
	}
	if !errors.Is(err, ErrUnknownCourier) {
		t.Fatalf("unknown courier error = %v, want ErrUnknownCourier", err)
	}
}

func TestPostgresRepositoryPreservesCreationTimeAndOrder(t *testing.T) {
	repository, _, _, user := setupWarningRepository(t)
	ctx := context.Background()
	newerAt := time.Date(2026, time.October, 12, 10, 30, 0, 0, time.UTC)
	olderAt := newerAt.Add(-24 * time.Hour)
	olderEventID := uuid.New()

	// The older cancellation arrives after the newer one.
	for _, warning := range []Warning{
		{UserID: user.ID, RequestID: uuid.New(), Reason: "newer cancellation", Status: StatusActive, CreatedAt: newerAt},
		{UserID: user.ID, RequestID: uuid.New(), Reason: "delayed cancellation", Status: StatusActive, SourceEventID: &olderEventID, CreatedAt: olderAt},
	} {
		if created, err := repository.Create(ctx, warning); err != nil || !created {
			t.Fatalf("create warning: created=%v err=%v", created, err)
		}
	}

	warnings, err := repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 2 {
		t.Fatalf("listed warnings=%+v err=%v", warnings, err)
	}
	if !warnings[0].CreatedAt.Equal(newerAt) || !warnings[1].CreatedAt.Equal(olderAt) {
		t.Fatalf(
			"warning times = [%s, %s], want [%s, %s]",
			warnings[0].CreatedAt,
			warnings[1].CreatedAt,
			newerAt,
			olderAt,
		)
	}

	// Replaying an event must not change the original issue date.
	created, err := repository.Create(ctx, Warning{
		UserID: user.ID, RequestID: uuid.New(), Reason: "retry payload", Status: StatusActive,
		SourceEventID: &olderEventID, CreatedAt: newerAt.Add(24 * time.Hour),
	})
	if err != nil || created {
		t.Fatalf("duplicate event: created=%v err=%v", created, err)
	}
	warnings, err = repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(warnings) != 2 || !warnings[0].CreatedAt.Equal(newerAt) ||
		!warnings[1].CreatedAt.Equal(olderAt) {
		t.Fatalf("replay changed warning history: warnings=%+v err=%v", warnings, err)
	}
}

func TestPostgresRepositoryListPagesAndCountsVisibleWarnings(t *testing.T) {
	repository, db, _, user := setupWarningRepository(t)
	ctx := context.Background()
	createdAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		created, err := repository.Create(ctx, Warning{
			UserID: user.ID, RequestID: uuid.New(), Reason: "warning", Status: StatusActive,
			CreatedAt: createdAt.Add(time.Duration(i) * time.Hour),
		})
		if err != nil || !created {
			t.Fatalf("create warning %d: created=%v err=%v", i, created, err)
		}
	}

	all, err := repository.ListByUser(ctx, user.ID, 0, 100)
	if err != nil || len(all) != 5 {
		t.Fatalf("list all warnings: warnings=%d err=%v", len(all), err)
	}
	if err := db.Exec("UPDATE account_warnings SET deleted_at = now() WHERE id = ?", all[0].ID).Error; err != nil {
		t.Fatal(err)
	}

	page, err := repository.ListByUser(ctx, user.ID, 0, 2)
	if err != nil || len(page) != 2 || page[0].ID != all[1].ID || page[1].ID != all[2].ID {
		t.Fatalf("page warnings=%+v err=%v", page, err)
	}
	total, err := repository.CountByUser(ctx, user.ID)
	if err != nil || total != 4 {
		t.Fatalf("visible warning count=%d err=%v, want 4", total, err)
	}
}
