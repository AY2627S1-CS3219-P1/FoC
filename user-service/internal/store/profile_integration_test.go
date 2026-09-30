// Store tests for profile updates, user deletion and warnings; see auth_integration_test.go for setup.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

var missingID = uuid.New()

func TestUsersUpdateAndList(t *testing.T) {
	ctx := context.Background()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	handle := "user_handle"
	if err := store.Users.Update(ctx, user.ID, UserUpdate{TelegramHandle: &handle}); err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	if err := store.Users.Update(ctx, user.ID, UserUpdate{DisplayName: &name, UpdatedBy: &user.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != name || got.TelegramHandle == nil || *got.TelegramHandle != handle ||
		got.UpdatedBy == nil || *got.UpdatedBy != user.ID {
		t.Fatalf("partial update: %+v", got)
	}
	empty := ""
	if err := store.Users.Update(ctx, user.ID, UserUpdate{TelegramHandle: &empty}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil || got.TelegramHandle != nil {
		t.Fatalf("clear telegram: %+v, %v", got, err)
	}
	if err := store.Users.Update(ctx, missingID, UserUpdate{DisplayName: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing user: %v", err)
	}

	users, total, err := store.Users.List(ctx, ListParams{})
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("list zero params: %d users, total %d, %v", len(users), total, err)
	}
}

func TestUsersDelete(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	sessionHash := testDigest("delete-session")
	if err := store.Sessions.Create(ctx, &models.Session{UserID: user.ID, TokenHash: sessionHash[:], LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFavourite(ctx, user.ID, uuid.New(), now); err != nil {
		t.Fatal(err)
	}

	if err := db.Create(&models.AdminBootstrap{Singleton: true, UserID: user.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Users.Delete(ctx, user.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete bootstrap admin: %v", err)
	}
	if err := db.Delete(&models.AdminBootstrap{}, "singleton").Error; err != nil {
		t.Fatal(err)
	}

	if err := store.Users.Delete(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user still visible: %v", err)
	}
	var sessions, favourites int64
	if err := db.Model(&models.Session{}).Where("user_id = ?", user.ID).Count(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.FavouriteSupplier{}).Where("user_id = ?", user.ID).Count(&favourites).Error; err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || favourites != 0 {
		t.Fatalf("delete left sessions=%d favourites=%d", sessions, favourites)
	}
	if err := store.Users.Delete(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated delete: %v", err)
	}
	createStoreUser(t, store, "user@example.com")
}

func TestAdminWarningsAndUserstamps(t *testing.T) {
	store, _ := setupAuthStore(t)
	admin := createStoreUser(t, store, "admin@example.com")
	user := createStoreUser(t, store, "user@example.com")
	ctx := context.Background()

	ev := uuid.New()
	first := &models.AccountWarning{UserID: user.ID, RequestID: uuid.New(), Reason: "late", SourceEventID: &ev}
	first.CreatedBy = &admin.ID
	created, err := store.CreateWarning(ctx, first)
	if err != nil || !created {
		t.Fatalf("create warning: %v, %v", created, err)
	}
	if first.CreatedBy == nil || *first.CreatedBy != admin.ID {
		t.Fatalf("created_by = %v, want %v", first.CreatedBy, admin.ID)
	}

	replay := &models.AccountWarning{UserID: user.ID, RequestID: uuid.New(), Reason: "late", SourceEventID: &ev}
	replay.ID = missingID
	created, err = store.CreateWarning(ctx, replay)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("replay: created=%v id=%v err=%v, want id %v", created, replay.ID, err, first.ID)
	}

	reason := "overturned"
	removed, err := store.RemoveWarning(ctx, first.ID, time.Now().UTC(), &reason, nil, &admin.ID)
	if err != nil || removed.Status != models.WarningRemoved || removed.UpdatedBy == nil || *removed.UpdatedBy != admin.ID {
		t.Fatalf("remove warning: %+v, %v", removed, err)
	}
	if _, err := store.RemoveWarning(ctx, first.ID, time.Now().UTC(), &reason, nil, &admin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated remove: %v", err)
	}
}
