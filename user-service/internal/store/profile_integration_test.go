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

	phone := "+6591234567"
	if err := store.Users.Update(ctx, user.ID, UserUpdate{PhoneNumber: &phone}); err != nil {
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
	if got.DisplayName != name || got.PhoneNumber == nil || *got.PhoneNumber != phone ||
		got.UpdatedBy == nil || *got.UpdatedBy != user.ID {
		t.Fatalf("partial update: %+v", got)
	}
	empty := ""
	if err := store.Users.Update(ctx, user.ID, UserUpdate{PhoneNumber: &empty}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil || got.PhoneNumber != nil {
		t.Fatalf("clear phone number: %+v, %v", got, err)
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
