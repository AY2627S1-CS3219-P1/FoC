package warning

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

func TestPostgresRepositoryCreateListAndRemove(t *testing.T) {
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
	ctx := context.Background()
	eventID, requestID := uuid.New(), uuid.New()
	createdBy := actor.ID
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
	warnings, err := repository.ListByUser(ctx, user.ID)
	if err != nil || len(warnings) != 1 || warnings[0].RequestID != requestID || warnings[0].CreatedBy == nil || *warnings[0].CreatedBy != actor.ID {
		t.Fatalf("listed warnings=%+v err=%v", warnings, err)
	}

	removedAt := time.Now().UTC()
	reason := "appeal overturned"
	updatedBy := actor.ID
	removed, err := repository.Remove(ctx, warnings[0].ID, removedAt, &reason, nil, &updatedBy)
	if err != nil || removed.Status != StatusRemoved || removed.RemovedAt == nil || removed.UpdatedBy == nil || *removed.UpdatedBy != actor.ID {
		t.Fatalf("remove warning: %+v err=%v", removed, err)
	}
	if _, err := repository.Remove(ctx, warnings[0].ID, removedAt, &reason, nil, &updatedBy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat removal: %v", err)
	}
	warnings, err = repository.ListByUser(ctx, user.ID)
	if err != nil || len(warnings) != 1 || warnings[0].Status != StatusRemoved {
		t.Fatalf("removed warning missing from user history: %+v err=%v", warnings, err)
	}
}
