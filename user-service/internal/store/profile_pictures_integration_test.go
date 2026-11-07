// Store tests for pending profile-picture uploads; see auth_integration_test.go
// for the throwaway-database setup.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func profilePictureUpload(userID uuid.UUID, createdAt time.Time) *models.ProfilePictureUpload {
	return &models.ProfilePictureUpload{
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

	upload := profilePictureUpload(user.ID, time.Now().UTC())
	if err := store.ProfilePictures.InsertUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ProfilePictures.GetUpload(ctx, user.ID, upload.ID); err != nil ||
		got.ObjectKey != upload.ObjectKey {
		t.Fatalf("get upload: %+v, %v", got, err)
	}
	if _, err := store.ProfilePictures.GetUpload(ctx, other.ID, upload.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("foreign upload lookup: %v", err)
	}
	if _, err := store.ProfilePictures.SetUserPicture(
		ctx,
		other.ID,
		upload.ID,
		other.ID,
	); !errors.Is(
		err,
		ErrUploadNotFound,
	) {
		t.Fatalf("foreign upload apply: %v", err)
	}

	oldKey, err := store.ProfilePictures.SetUserPicture(ctx, user.ID, upload.ID, user.ID)
	if err != nil || oldKey != nil {
		t.Fatalf("apply picture: %v, %v", oldKey, err)
	}
	got, err := store.GetByID(ctx, user.ID)
	if err != nil || got.ProfilePictureKey == nil || *got.ProfilePictureKey != upload.ObjectKey {
		t.Fatalf("current picture: %+v, %v", got, err)
	}
	if _, err := store.ProfilePictures.GetUpload(ctx, user.ID, upload.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("consumed upload still visible: %v", err)
	}

	oldKey, err = store.ProfilePictures.ClearUserPicture(ctx, user.ID, user.ID)
	if err != nil || oldKey == nil || *oldKey != upload.ObjectKey {
		t.Fatalf("clear picture: %v, %v", oldKey, err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil || got.ProfilePictureKey != nil {
		t.Fatalf("cleared picture: %+v, %v", got, err)
	}
}

func TestProfilePicturesSuspendedUserCannotApply(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "suspended@example.com")
	upload := profilePictureUpload(user.ID, time.Now().UTC())
	if err := store.ProfilePictures.InsertUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).
		Update("role", models.RoleSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProfilePictures.SetUserPicture(
		ctx,
		user.ID,
		upload.ID,
		user.ID,
	); !errors.Is(
		err,
		ErrRoleConflict,
	) {
		t.Fatalf("suspended apply: %v", err)
	}
	if _, err := store.ProfilePictures.ClearUserPicture(ctx, user.ID, user.ID); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("suspended clear: %v", err)
	}
}

func TestProfilePicturesCleanupClaimsExpiredUploads(t *testing.T) {
	ctx := context.Background()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "cleanup@example.com")
	old := profilePictureUpload(user.ID, time.Now().UTC().Add(-48*time.Hour))
	fresh := profilePictureUpload(user.ID, time.Now().UTC())
	for _, upload := range []*models.ProfilePictureUpload{old, fresh} {
		if err := store.ProfilePictures.InsertUpload(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := store.ProfilePictures.ClaimExpiredUploads(ctx, time.Now().UTC().Add(-24*time.Hour), 50)
	if err != nil || len(claimed) != 1 || claimed[0].ID != old.ID || !claimed[0].Deleting {
		t.Fatalf("claim: %+v, %v", claimed, err)
	}
	if _, err := store.ProfilePictures.GetUpload(ctx, user.ID, old.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("claimed upload still applyable: %v", err)
	}
	reclaimed, err := store.ProfilePictures.ClaimExpiredUploads(ctx, time.Now().UTC().Add(-24*time.Hour), 50)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != old.ID {
		t.Fatalf("re-claim after failed delete: %+v, %v", reclaimed, err)
	}
	if err := store.ProfilePictures.DeleteUploads(ctx, []uuid.UUID{old.ID}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := store.db.Model(&models.ProfilePictureUpload{}).Where("id = ?", old.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("claimed row retained: %d", count)
	}
	if _, err := store.ProfilePictures.GetUpload(ctx, user.ID, fresh.ID); err != nil {
		t.Fatalf("fresh upload was reaped: %v", err)
	}
}
