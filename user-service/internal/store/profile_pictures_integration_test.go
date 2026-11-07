// Store tests for profile-picture rows; see auth_integration_test.go for the
// throwaway-database setup.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func profilePictureRow(userID uuid.UUID, createdAt time.Time) *models.ProfilePicture {
	return &models.ProfilePicture{
		ID:          uuid.New(),
		UserID:      userID,
		ObjectKey:   "profile/picture/" + userID.String() + "/" + uuid.NewString(),
		ContentType: "image/png",
		SizeBytes:   4,
		CreatedAt:   createdAt,
	}
}

func TestProfilePicturesPersistence(t *testing.T) {
	ctx := context.Background()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "pictures@example.com")
	other := createStoreUser(t, store, "other@example.com")

	first := profilePictureRow(user.ID, time.Now().UTC())
	if err := store.ProfilePictures.InsertPending(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ProfilePictures.GetPending(ctx, user.ID, first.ID); err != nil ||
		got.ObjectKey != first.ObjectKey || got.State != models.ProfilePicturePending {
		t.Fatalf("get pending: %+v, %v", got, err)
	}
	if _, err := store.ProfilePictures.GetPending(ctx, other.ID, first.ID); !errors.Is(err, ErrPictureNotFound) {
		t.Fatalf("foreign pending lookup: %v", err)
	}
	if _, err := store.ProfilePictures.Activate(
		ctx,
		other.ID,
		first.ID,
		other.ID,
	); !errors.Is(
		err,
		ErrPictureNotFound,
	) {
		t.Fatalf("foreign activate: %v", err)
	}

	oldKey, err := store.ProfilePictures.Activate(ctx, user.ID, first.ID, user.ID)
	if err != nil || oldKey != nil {
		t.Fatalf("activate first: %v, %v", oldKey, err)
	}
	got, err := store.GetByID(ctx, user.ID)
	if err != nil || got.ProfilePictureID == nil || *got.ProfilePictureID != first.ID {
		t.Fatalf("current pointer: %+v, %v", got, err)
	}
	if picture, err := store.ProfilePictures.GetPictureByID(ctx, first.ID); err != nil ||
		picture.State != models.ProfilePictureActive {
		t.Fatalf("active row: %+v, %v", picture, err)
	}

	second := profilePictureRow(user.ID, time.Now().UTC())
	if err := store.ProfilePictures.InsertPending(ctx, second); err != nil {
		t.Fatal(err)
	}
	oldKey, err = store.ProfilePictures.Activate(ctx, user.ID, second.ID, user.ID)
	if err != nil || oldKey == nil || *oldKey != first.ObjectKey {
		t.Fatalf("replace: %v, %v", oldKey, err)
	}
	oldRow, err := store.ProfilePictures.GetPictureByID(ctx, first.ID)
	if err != nil || oldRow.State != models.ProfilePictureDeleting {
		t.Fatalf("old row: %+v, %v", oldRow, err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil ||
		got.ProfilePictureID == nil || *got.ProfilePictureID != second.ID {
		t.Fatalf("replacement pointer: %+v, %v", got, err)
	}

	oldKey, err = store.ProfilePictures.Deactivate(ctx, user.ID, user.ID)
	if err != nil || oldKey == nil || *oldKey != second.ObjectKey {
		t.Fatalf("remove: %v, %v", oldKey, err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil || got.ProfilePictureID != nil {
		t.Fatalf("cleared pointer: %+v, %v", got, err)
	}
	before, err := store.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldKey, err = store.ProfilePictures.Deactivate(ctx, user.ID, user.ID)
	if err != nil || oldKey != nil {
		t.Fatalf("repeated remove: %v, %v", oldKey, err)
	}
	after, err := store.GetByID(ctx, user.ID)
	if err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("no-op remove touched the user: %+v, %v", after, err)
	}
}

func TestProfilePicturesSuspendedUserCannotChange(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "suspended@example.com")
	picture := profilePictureRow(user.ID, time.Now().UTC())
	if err := store.ProfilePictures.InsertPending(ctx, picture); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).
		Update("role", models.RoleSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProfilePictures.Activate(ctx, user.ID, picture.ID, user.ID); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("suspended activate: %v", err)
	}
	if _, err := store.ProfilePictures.Deactivate(ctx, user.ID, user.ID); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("suspended remove: %v", err)
	}
}

func TestProfilePicturesCleanupClaimsExpiredPending(t *testing.T) {
	ctx := context.Background()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "cleanup@example.com")
	old := profilePictureRow(user.ID, time.Now().UTC().Add(-48*time.Hour))
	fresh := profilePictureRow(user.ID, time.Now().UTC())
	for _, picture := range []*models.ProfilePicture{old, fresh} {
		if err := store.ProfilePictures.InsertPending(ctx, picture); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := store.ProfilePictures.ClaimExpiredPending(ctx, time.Now().UTC().Add(-24*time.Hour), 50)
	if err != nil || len(claimed) != 1 || claimed[0].ID != old.ID ||
		claimed[0].State != models.ProfilePictureDeleting {
		t.Fatalf("claim: %+v, %v", claimed, err)
	}
	if _, err := store.ProfilePictures.GetPending(ctx, user.ID, old.ID); !errors.Is(err, ErrPictureNotFound) {
		t.Fatalf("claimed row still pending: %v", err)
	}
	deleting, err := store.ProfilePictures.ListDeleting(ctx, 50)
	if err != nil || len(deleting) != 1 || deleting[0].ID != old.ID {
		t.Fatalf("list deleting: %+v, %v", deleting, err)
	}
	if err := store.ProfilePictures.DeletePicture(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProfilePictures.GetPictureByID(ctx, old.ID); !errors.Is(err, ErrPictureNotFound) {
		t.Fatalf("claimed row retained: %v", err)
	}
	if _, err := store.ProfilePictures.GetPending(ctx, user.ID, fresh.ID); err != nil {
		t.Fatalf("fresh upload was reaped: %v", err)
	}
}
