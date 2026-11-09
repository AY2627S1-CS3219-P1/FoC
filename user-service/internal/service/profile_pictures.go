// Profile pictures move pending -> active -> deleting; the row ID is the reference.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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

// ProfilePictureStore is the persistence surface the profile service consumes.
type ProfilePictureStore interface {
	InsertPending(context.Context, *models.ProfilePicture) error
	GetPending(context.Context, uuid.UUID, uuid.UUID) (*models.ProfilePicture, error)
	GetPictureByID(context.Context, uuid.UUID) (*models.ProfilePicture, error)
	Activate(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*string, error)
	Deactivate(context.Context, uuid.UUID, uuid.UUID) (*string, error)
	ClaimExpiredPending(context.Context, time.Time, int) ([]models.ProfilePicture, error)
	ListDeleting(context.Context, int) ([]models.ProfilePicture, error)
	DeletePicture(context.Context, uuid.UUID) error
}

// ProfilePictureUpload is the ID of one pending row.
type ProfilePictureUpload struct{ ID uuid.UUID }

// CreateProfilePictureUpload records a pending row and returns its PUT grant.
func (s *ProfileService) CreateProfilePictureUpload(
	ctx context.Context,
	actorID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (ProfilePictureUpload, images.UploadGrant, error) {
	if s.Pictures == nil || s.Storage == nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, profilePictureUnavailable(
			errProfilePictureStorageDisabled,
		)
	}
	if !validProfilePictureContentType(contentType) {
		return ProfilePictureUpload{}, images.UploadGrant{}, invalidProfilePicture(
			"content type must be image/jpeg, image/png, image/webp, image/heic or image/heif",
		)
	}
	if sizeBytes < 1 || sizeBytes > MaxProfilePictureBytes {
		return ProfilePictureUpload{}, images.UploadGrant{}, invalidProfilePicture(
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
		return ProfilePictureUpload{}, images.UploadGrant{}, fmt.Errorf("generate picture id: %w", err)
	}
	key := profilePictureKey(actorID, id)
	// Sign before persisting: a failed sign leaves no orphan pending row.
	grant, err := s.Storage.CreateUpload(ctx, images.UploadRequest{
		Key:         key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	})
	if err != nil {
		wrapped := fmt.Errorf("sign profile picture upload grant: %w", err)
		if errors.Is(err, images.ErrStorageUnavailable) {
			return ProfilePictureUpload{}, images.UploadGrant{}, profilePictureTemporarilyUnavailable(wrapped)
		}
		return ProfilePictureUpload{}, images.UploadGrant{}, profilePictureUnavailable(wrapped)
	}
	picture := &models.ProfilePicture{
		ID:          id,
		UserID:      actorID,
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		State:       models.ProfilePicturePending,
		CreatedAt:   s.now(),
	}
	if err := s.Pictures.InsertPending(ctx, picture); err != nil {
		return ProfilePictureUpload{}, images.UploadGrant{}, fmt.Errorf("store profile picture: %w", err)
	}
	return ProfilePictureUpload{ID: id}, grant, nil
}

// UpdateMyProfilePicture validates the upload and makes the row active; retries succeed.
func (s *ProfileService) UpdateMyProfilePicture(
	ctx context.Context,
	actorID, pictureID uuid.UUID,
) (ProfileView, error) {
	if s.Pictures == nil || s.Storage == nil {
		return ProfileView{}, profilePictureUnavailable(errProfilePictureStorageDisabled)
	}
	user, err := s.userByID(ctx, actorID)
	if err != nil {
		return ProfileView{}, err
	}
	if user.IsSuspended() {
		return ProfileView{}, ErrPermissionDenied
	}
	picture, err := s.Pictures.GetPending(ctx, actorID, pictureID)
	if errors.Is(err, store.ErrPictureNotFound) {
		if s.alreadyActive(ctx, actorID, pictureID) {
			return s.GetMyProfile(ctx, actorID)
		}
		return ProfileView{}, profilePictureNotReady(
			err, pictureID, "picture was not uploaded by this user or is no longer pending",
		)
	}
	if err != nil {
		return ProfileView{}, fmt.Errorf("get profile picture: %w", err)
	}

	info, err := s.Storage.Inspect(ctx, picture.ObjectKey)
	if errors.Is(err, images.ErrObjectNotFound) {
		return ProfileView{}, profilePictureNotReady(
			err, pictureID, "picture upload is missing; upload it again",
		)
	}
	if err != nil {
		wrapped := fmt.Errorf("inspect profile picture object: %w", err)
		if errors.Is(err, images.ErrStorageUnavailable) {
			return ProfileView{}, profilePictureTemporarilyUnavailable(wrapped)
		}
		return ProfileView{}, profilePictureUnavailable(wrapped)
	}
	if !info.Exists || info.Size != picture.SizeBytes || info.ContentType != picture.ContentType ||
		!validProfilePictureContentType(info.ContentType) || info.Size < 1 || info.Size > MaxProfilePictureBytes {
		return ProfileView{}, profilePictureNotReady(
			errInvalidProfilePicture, pictureID, "picture upload is incomplete or invalid; upload it again",
		)
	}

	oldKey, err := s.Pictures.Activate(ctx, actorID, pictureID, actorID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPictureNotFound):
			if s.alreadyActive(ctx, actorID, pictureID) {
				return s.GetMyProfile(ctx, actorID)
			}
			return ProfileView{}, profilePictureNotReady(
				err, pictureID, "picture is no longer available; upload it again",
			)
		case errors.Is(err, store.ErrRoleConflict):
			return ProfileView{}, ErrPermissionDenied
		case errors.Is(err, store.ErrNotFound):
			return ProfileView{}, ErrUnauthenticated
		default:
			return ProfileView{}, fmt.Errorf("activate profile picture: %w", err)
		}
	}
	if oldKey != nil {
		s.deletePictureObject(ctx, *oldKey)
	}
	return s.GetMyProfile(ctx, actorID)
}

// RemoveMyProfilePicture clears the pointer and retires the row; idempotent.
func (s *ProfileService) RemoveMyProfilePicture(
	ctx context.Context,
	actorID uuid.UUID,
) (ProfileView, error) {
	if s.Pictures == nil || s.Storage == nil {
		return ProfileView{}, profilePictureUnavailable(errProfilePictureStorageDisabled)
	}
	oldKey, err := s.Pictures.Deactivate(ctx, actorID, actorID)
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

// CleanupOnce reaps expired pending rows and retries deleting rows.
func (s *ProfileService) CleanupOnce(ctx context.Context) error {
	if s.Pictures == nil || s.Storage == nil {
		return nil
	}
	if _, err := s.Pictures.ClaimExpiredPending(
		ctx, s.now().Add(-profilePictureOrphanAfter), profilePictureCleanupBatch,
	); err != nil {
		return fmt.Errorf("claim expired profile pictures: %w", err)
	}
	rows, err := s.Pictures.ListDeleting(ctx, profilePictureCleanupBatch)
	if err != nil {
		return fmt.Errorf("list deleting profile pictures: %w", err)
	}
	var failures []error
	for _, row := range rows {
		if err := s.Storage.Delete(ctx, row.ObjectKey); err != nil &&
			!errors.Is(err, images.ErrObjectNotFound) {
			failures = append(failures, fmt.Errorf("delete profile picture object %s: %w", row.ID, err))
			continue
		}
		if err := s.Pictures.DeletePicture(ctx, row.ID); err != nil {
			failures = append(failures, fmt.Errorf("delete profile picture row %s: %w", row.ID, err))
		}
	}
	return errors.Join(failures...)
}

// profileView mints a download grant; missing or unreadable pictures degrade to no URL.
func (s *ProfileService) profileView(ctx context.Context, user models.User) ProfileView {
	view := ProfileView{User: user}
	if user.ProfilePictureID == nil || s.Pictures == nil || s.Storage == nil {
		return view
	}
	picture, err := s.Pictures.GetPictureByID(ctx, *user.ProfilePictureID)
	if err != nil {
		if !errors.Is(err, store.ErrPictureNotFound) {
			slog.WarnContext(ctx, "profile picture row unavailable", "user_id", user.ID, "err", err)
		}
		return view
	}
	if picture.State != models.ProfilePictureActive {
		return view
	}
	grant, err := s.Storage.CreateDownload(ctx, picture.ObjectKey)
	if err != nil {
		slog.WarnContext(ctx, "profile picture URL unavailable", "user_id", user.ID, "err", err)
		return view
	}
	view.PictureURL = grant.URL
	view.PictureExpiresAt = grant.ExpiresAt
	return view
}

// alreadyActive recognises a commit that happened after the caller's first read.
func (s *ProfileService) alreadyActive(ctx context.Context, actorID, pictureID uuid.UUID) bool {
	user, err := s.userByID(ctx, actorID)
	return err == nil && user.ProfilePictureID != nil && *user.ProfilePictureID == pictureID
}

func (s *ProfileService) deletePictureObject(ctx context.Context, key string) {
	if s.Storage == nil {
		return
	}
	if err := s.Storage.Delete(ctx, key); err != nil {
		slog.WarnContext(ctx, "delete superseded profile picture object", "key", key, "err", err)
	}
}

func (s *ProfileService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// profilePictureKey is deterministic so a retried submit recognises its own result.
func profilePictureKey(userID, pictureID uuid.UUID) string {
	return profilePictureKeyPrefix + userID.String() + "/" + pictureID.String()
}

// validProfilePictureContentType mirrors the migration's CHECK constraint.
func validProfilePictureContentType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}
