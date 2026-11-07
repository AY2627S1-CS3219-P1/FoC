// Profile picture uploads: pending grants are applied by the same call that
// validates the stored object and updates the user's current picture key.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

const (
	// MaxProfilePictureBytes matches the repository-wide image limit.
	MaxProfilePictureBytes int64 = 10000000

	profilePictureOrphanAfter  = 24 * time.Hour
	profilePictureCleanupBatch = 50
	profilePictureKeyPrefix    = "profile/picture/"
)

var errProfilePictureStorageDisabled = errors.New("profile picture storage is not configured")

// ProfilePictureStorage is the consumer-defined object-storage boundary. The
// domain works with opaque keys and never imports a provider SDK.
type ProfilePictureStorage interface {
	CreateUpload(context.Context, images.UploadRequest) (images.UploadGrant, error)
	Inspect(context.Context, string) (images.ObjectInfo, error)
	CreateDownload(context.Context, string) (images.DownloadGrant, error)
	Delete(context.Context, string) error
}

// ProfilePictureStore is the pending-upload and current-picture persistence
// surface consumed by the profile service.
type ProfilePictureStore interface {
	InsertUpload(context.Context, *models.ProfilePictureUpload) error
	GetUpload(context.Context, uuid.UUID, uuid.UUID) (*models.ProfilePictureUpload, error)
	SetUserPicture(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*string, error)
	ClearUserPicture(context.Context, uuid.UUID, uuid.UUID) (*string, error)
	ClaimExpiredUploads(context.Context, time.Time, int) ([]models.ProfilePictureUpload, error)
	DeleteUploads(context.Context, []uuid.UUID) error
}

// ProfilePictureUpload is the durable identity of one pending upload.
type ProfilePictureUpload struct {
	ID        uuid.UUID
	ObjectKey string
}

// CreateProfilePictureUpload validates the caller and shape, records a pending
// upload, and returns the pre-signed PUT grant.
func (s *ProfileService) CreateProfilePictureUpload(
	ctx context.Context,
	actorID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (ProfilePictureUpload, images.UploadGrant, error) {
	if s.Pictures == nil || s.Storage == nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, errs.WrapFailedPreconditionError(
			errProfilePictureStorageDisabled, "profile picture uploads are not available",
		)
	}
	if !validProfilePictureContentType(contentType) {
		return ProfilePictureUpload{}, images.UploadGrant{}, errs.NewBadRequestError(
			"content type must be image/jpeg, image/png, image/webp, image/heic or image/heif",
		)
	}
	if sizeBytes < 1 || sizeBytes > MaxProfilePictureBytes {
		return ProfilePictureUpload{}, images.UploadGrant{}, errs.NewBadRequestError(
			"picture size must be between 1 and 10000000 bytes",
		)
	}
	user, err := s.userByID(ctx, actorID)
	if err != nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, err
	}
	if user.IsSuspended() {
		return ProfilePictureUpload{}, images.UploadGrant{}, ErrPermissionDenied
	}

	id, err := uuid.NewV7()
	if err != nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, fmt.Errorf("generate picture upload id: %w", err)
	}
	key := profilePictureKey(actorID, id)
	upload := &models.ProfilePictureUpload{
		ID:          id,
		UserID:      actorID,
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		CreatedAt:   s.now(),
	}
	if err := s.Pictures.InsertUpload(ctx, upload); err != nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, fmt.Errorf("store profile picture upload: %w", err)
	}
	grant, err := s.Storage.CreateUpload(ctx, images.UploadRequest{
		Key:         key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	})
	if err != nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, errs.WrapUnavailableError(
			err, "profile picture storage is unavailable",
		)
	}
	return ProfilePictureUpload{ID: id, ObjectKey: key}, grant, nil
}

// UpdateMyProfilePicture validates the caller's pending upload against storage
// and makes it current. Repeating the call after a lost response succeeds when
// the deterministic key is already current.
func (s *ProfileService) UpdateMyProfilePicture(
	ctx context.Context,
	actorID, uploadID uuid.UUID,
) (ProfileView, error) {
	if s.Pictures == nil || s.Storage == nil {
		return ProfileView{}, errs.WrapFailedPreconditionError(
			errProfilePictureStorageDisabled, "profile picture uploads are not available",
		)
	}
	user, err := s.userByID(ctx, actorID)
	if err != nil {
		return ProfileView{}, err
	}
	if user.IsSuspended() {
		return ProfileView{}, ErrPermissionDenied
	}
	upload, err := s.Pictures.GetUpload(ctx, actorID, uploadID)
	if errors.Is(err, store.ErrUploadNotFound) {
		if s.pictureKeyMatches(ctx, actorID, profilePictureKey(actorID, uploadID)) {
			return s.GetMyProfile(ctx, actorID)
		}
		return ProfileView{}, errs.WrapBadRequestError(err, "picture upload was not started by this user")
	}
	if err != nil {
		return ProfileView{}, fmt.Errorf("get profile picture upload: %w", err)
	}

	info, err := s.Storage.Inspect(ctx, upload.ObjectKey)
	if errors.Is(err, images.ErrObjectNotFound) {
		return ProfileView{}, errs.WrapBadRequestError(err, "picture upload is missing; upload it again")
	}
	if err != nil {
		return ProfileView{}, errs.WrapUnavailableError(err, "profile picture storage is unavailable")
	}
	if !info.Exists || info.Size != upload.SizeBytes || info.ContentType != upload.ContentType ||
		!validProfilePictureContentType(info.ContentType) || info.Size < 1 || info.Size > MaxProfilePictureBytes {
		return ProfileView{}, errs.NewBadRequestError(
			"picture upload is incomplete or invalid; upload it again",
		)
	}

	oldKey, err := s.Pictures.SetUserPicture(ctx, actorID, uploadID, actorID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUploadNotFound):
			if s.pictureKeyMatches(ctx, actorID, upload.ObjectKey) {
				return s.GetMyProfile(ctx, actorID)
			}
			return ProfileView{}, errs.WrapBadRequestError(
				err, "picture upload is no longer available; upload it again",
			)
		case errors.Is(err, store.ErrRoleConflict):
			return ProfileView{}, ErrPermissionDenied
		case errors.Is(err, store.ErrNotFound):
			return ProfileView{}, ErrUnauthenticated
		default:
			return ProfileView{}, fmt.Errorf("set profile picture: %w", err)
		}
	}
	if oldKey != nil && *oldKey != upload.ObjectKey {
		s.deletePictureObject(ctx, *oldKey)
	}
	return s.GetMyProfile(ctx, actorID)
}

// RemoveMyProfilePicture clears the current picture. It is idempotent and does
// not require storage to be configured.
func (s *ProfileService) RemoveMyProfilePicture(
	ctx context.Context,
	actorID uuid.UUID,
) (ProfileView, error) {
	if s.Pictures == nil {
		return ProfileView{}, errs.WrapFailedPreconditionError(
			errProfilePictureStorageDisabled, "profile pictures are not available",
		)
	}
	oldKey, err := s.Pictures.ClearUserPicture(ctx, actorID, actorID)
	switch {
	case errors.Is(err, store.ErrRoleConflict):
		return ProfileView{}, ErrPermissionDenied
	case errors.Is(err, store.ErrNotFound):
		return ProfileView{}, ErrUnauthenticated
	case err != nil:
		return ProfileView{}, fmt.Errorf("remove profile picture: %w", err)
	}
	if oldKey != nil {
		s.deletePictureObject(ctx, *oldKey)
	}
	return s.GetMyProfile(ctx, actorID)
}

// CleanupOnce removes expired pending uploads. Rows are claimed first; object
// deletion is retried on the next pass if it fails.
func (s *ProfileService) CleanupOnce(ctx context.Context) error {
	if s.Pictures == nil || s.Storage == nil {
		return nil
	}
	rows, err := s.Pictures.ClaimExpiredUploads(
		ctx, s.now().Add(-profilePictureOrphanAfter), profilePictureCleanupBatch,
	)
	if err != nil {
		return fmt.Errorf("claim orphan profile picture uploads: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if err := s.Storage.Delete(ctx, row.ObjectKey); err != nil &&
			!errors.Is(err, images.ErrObjectNotFound) {
			return fmt.Errorf("delete orphan profile picture object: %w", err)
		}
		ids = append(ids, row.ID)
	}
	if err := s.Pictures.DeleteUploads(ctx, ids); err != nil {
		return fmt.Errorf("delete orphan profile picture rows: %w", err)
	}
	return nil
}

// profileView mints a fresh download grant for the current picture. A storage
// failure degrades to a profile without a picture instead of failing the read.
func (s *ProfileService) profileView(ctx context.Context, user models.User) ProfileView {
	view := ProfileView{User: user}
	if user.ProfilePictureKey == nil || s.Storage == nil {
		return view
	}
	grant, err := s.Storage.CreateDownload(ctx, *user.ProfilePictureKey)
	if err != nil {
		slog.WarnContext(ctx, "profile picture URL unavailable", "user_id", user.ID, "err", err)
		return view
	}
	view.PictureURL = grant.URL
	view.PictureExpiresAt = grant.ExpiresAt
	return view
}

func (s *ProfileService) pictureKeyMatches(ctx context.Context, actorID uuid.UUID, key string) bool {
	user, err := s.Users.GetByID(ctx, actorID)
	return err == nil && user.ProfilePictureKey != nil && *user.ProfilePictureKey == key
}

func (s *ProfileService) deletePictureObject(ctx context.Context, key string) {
	if s.Storage == nil {
		return
	}
	if err := s.Storage.Delete(ctx, key); err != nil {
		slog.WarnContext(ctx, "delete old profile picture object", "key", key, "err", err)
	}
}

func (s *ProfileService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// profilePictureKey is deterministic so a retried submit can recognize its own
// already-committed result without extra state.
func profilePictureKey(userID, uploadID uuid.UUID) string {
	return profilePictureKeyPrefix + userID.String() + "/" + uploadID.String()
}

// validProfilePictureContentType mirrors the migration's CHECK constraint.
func validProfilePictureContentType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}
