// Review photo lifecycle: staged uploads promoted to attached photos inside
// the review submission transaction. Storage access goes through the
// PhotoStorage port; the domain works with opaque object keys and treats
// grant URLs as short-lived credentials, never persisted state.
package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// MaxReviewPhotos caps one review's attachments; a review with zero photos is
// valid.
const MaxReviewPhotos = 5

const (
	// Repository-wide image convention: five accepted media types, decimal
	// 10MB ceiling, ten-minute upload grants.
	maxReviewPhotoBytes = 10000000
	photoUploadLifetime = 10 * time.Minute
	// Staging rows the uploader never attached are reaped after this grace
	// period, mirroring the order item image cleanup.
	photoOrphanAfter = 24 * time.Hour

	photoKeyPrefix      = "review/photo/"
	photoStateUploading = "uploading"
	photoStateAttached  = "attached"
)

// PhotoStorage is the consumer-defined storage port. Implementations sign
// grants, inspect stored metadata and mint short-lived download URLs; the
// domain never sees a storage SDK.
type PhotoStorage interface {
	CreateUpload(context.Context, images.UploadRequest) (images.UploadGrant, error)
	Inspect(context.Context, string) (images.ObjectInfo, error)
	CreateDownload(context.Context, string) (images.DownloadGrant, error)
	Delete(context.Context, string) error
}

var (
	errPhotoNotFound        = errors.New("review photo not found")
	errPhotoStorageDisabled = errors.New("photo storage is not configured")
)

// ReviewPhotoView is the read-model form of an attached photo: the stable ID
// plus a freshly minted, expiring download URL.
type ReviewPhotoView struct {
	ID          uuid.UUID
	URL         string
	ContentType string
	ExpiresAt   time.Time
}

// PhotoUpload is the staged upload admission: the durable row identity plus
// the grant the browser PUTs the bytes with.
type PhotoUpload struct {
	ID        uuid.UUID
	ObjectKey string
}

// CreateReviewPhotoUpload validates the request, signs a pre-signed PUT grant
// and persists a staging row owned by the caller. Signing happens first so a
// failed sign leaves nothing for the reaper to clean up; the stored row
// records the grant's exact content type and size, so the storage-enforced
// shape matches what attachment later verifies against.
func (s *Service) CreateReviewPhotoUpload(
	ctx context.Context,
	ownerID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (PhotoUpload, images.UploadGrant, error) {
	if s.photos == nil {
		return PhotoUpload{}, images.UploadGrant{}, uploadUnavailable(errPhotoStorageDisabled)
	}
	if !validPhotoContentType(contentType) {
		return PhotoUpload{}, images.UploadGrant{}, invalidPhoto(
			"content type must be image/jpeg, image/png, image/webp, image/heic or image/heif",
		)
	}
	if sizeBytes < 1 || sizeBytes > maxReviewPhotoBytes {
		return PhotoUpload{}, images.UploadGrant{}, invalidPhoto(
			"photo size must be between 1 and 10000000 bytes",
		)
	}

	id, err := newID()
	if err != nil {
		return PhotoUpload{}, images.UploadGrant{}, err
	}
	// Sign before persisting the staging row: a failed sign then leaves no
	// orphan row behind for the reaper to clean up.
	key := photoKeyPrefix + id.String()
	grant, err := s.photos.CreateUpload(ctx, images.UploadRequest{
		Key:         key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	})
	if err != nil {
		return PhotoUpload{}, images.UploadGrant{}, uploadUnavailable(fmt.Errorf("sign photo upload grant: %w", err))
	}
	photo := ReviewPhotoInput{
		ID:          id,
		OwnerID:     ownerID,
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	}
	photo.UploadExpiresAt = time.Now().UTC().Add(photoUploadLifetime)
	if err := s.repo.InsertReviewPhoto(ctx, photo); err != nil {
		return PhotoUpload{}, images.UploadGrant{}, err
	}
	return PhotoUpload{ID: id, ObjectKey: key}, grant, nil
}

// preparePhotos resolves the caller's staged photo IDs before the submission
// transaction: each row must belong to the caller, still be staging, and its
// stored object must exist with exactly the shape the grant signed. Storage
// inspection deliberately runs outside the transaction so it cannot hold
// database locks; attachment re-asserts state inside.
func (s *Service) preparePhotos(
	ctx context.Context,
	ownerID uuid.UUID,
	photoIDs []uuid.UUID,
) ([]ReviewPhoto, error) {
	if len(photoIDs) == 0 {
		return nil, nil
	}
	if s.photos == nil {
		return nil, uploadUnavailable(errPhotoStorageDisabled)
	}
	if len(photoIDs) > MaxReviewPhotos {
		return nil, errs.WrapBadRequestError(errInvalidReview, "a review may have at most 5 photos")
	}
	prepared := make([]ReviewPhoto, 0, len(photoIDs))
	seen := make(map[uuid.UUID]bool, len(photoIDs))
	for _, id := range photoIDs {
		if seen[id] {
			return nil, errs.WrapBadRequestError(errInvalidReview, "photo IDs must be unique")
		}
		seen[id] = true
		photo, err := s.repo.GetReviewPhoto(ctx, ownerID, id)
		if err != nil {
			if errors.Is(err, errPhotoNotFound) {
				return nil, errs.WrapBadRequestError(err, "photo was not staged by this user")
			}
			return nil, err
		}
		if photo.State != photoStateUploading {
			return nil, photoNotReady(errPhotoNotReady, id, "photo is no longer pending upload")
		}
		info, err := s.photos.Inspect(ctx, photo.ObjectKey)
		if errors.Is(err, images.ErrObjectNotFound) {
			return nil, photoNotReady(errPhotoNotReady, id, "photo upload is missing; upload it again")
		}
		if err != nil {
			wrapped := fmt.Errorf("inspect photo object: %w", err)
			if errors.Is(err, images.ErrStorageUnavailable) {
				return nil, uploadTemporarilyUnavailable(wrapped)
			}
			return nil, uploadUnavailable(wrapped)
		}
		if !info.Exists || info.Size != photo.SizeBytes || info.ContentType != photo.ContentType {
			return nil, photoNotReady(errPhotoNotReady, id, "photo upload is incomplete or invalid; upload it again")
		}
		prepared = append(prepared, photo)
	}
	return prepared, nil
}

// attachPhotos promotes the prepared rows inside the submission transaction,
// in list order. The repository update is guarded on ownership, staging state
// and the inspected shape; any zero-row update means the photo changed between
// preparation and attach, failing the whole submission.
func (s *Service) attachPhotos(
	ctx context.Context,
	q *reviewdb.Queries,
	ownerID, reviewID uuid.UUID,
	photos []ReviewPhoto,
) error {
	for ordinal, photo := range photos {
		n, err := s.repo.AttachReviewPhoto(ctx, q, ownerID, reviewID, ordinal, photo)
		if err != nil {
			return err
		}
		if n != 1 {
			return photoNotReady(
				errPhotoNotReady,
				photo.ID,
				"a photo is no longer available; remove it and submit again",
			)
		}
	}
	return nil
}

// fillPhotos attaches freshly minted download URLs to a page of reviews.
// Reviews without photos, or a service without storage configured, pass
// through unchanged.
func (s *Service) fillPhotos(ctx context.Context, reviews []Review) ([]Review, error) {
	if len(reviews) == 0 || s.photos == nil {
		return reviews, nil
	}
	ids := make([]uuid.UUID, 0, len(reviews))
	for _, review := range reviews {
		ids = append(ids, review.ID)
	}
	photos, err := s.repo.ListReviewPhotosForReviews(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return reviews, nil
	}
	byReview := make(map[uuid.UUID][]ReviewPhotoView, len(reviews))
	for _, photo := range photos {
		grant, err := s.photos.CreateDownload(ctx, photo.ObjectKey)
		if err != nil {
			return nil, fmt.Errorf("sign photo download: %w", err)
		}
		byReview[photo.ReviewID] = append(byReview[photo.ReviewID], ReviewPhotoView{
			ID:          photo.ID,
			URL:         grant.URL,
			ContentType: photo.ContentType,
			ExpiresAt:   grant.ExpiresAt,
		})
	}
	for i := range reviews {
		reviews[i].Photos = byReview[reviews[i].ID]
	}
	return reviews, nil
}

// CleanupOnce reaps expired staging rows: claim a batch, delete the stored
// objects, then delete the rows. Every step is retryable; the deleting state
// fences attachment in between.
func (s *Service) CleanupOnce(ctx context.Context) error {
	if s.photos == nil {
		return nil
	}
	now := time.Now().UTC()
	claimed, err := s.repo.ClaimOrphanReviewPhotos(ctx, now.Add(-photoOrphanAfter), now)
	if err != nil {
		return fmt.Errorf("claim orphan review photos: %w", err)
	}
	for _, row := range claimed {
		if err := s.photos.Delete(ctx, row.ObjectKey); err != nil {
			return fmt.Errorf("delete orphan review photo object: %w", err)
		}
		if _, err := s.repo.DeleteOrphanReviewPhoto(ctx, row.ID); err != nil {
			return fmt.Errorf("delete orphan review photo row: %w", err)
		}
	}
	return nil
}

// validPhotoContentType mirrors the migration's CHECK constraint.
func validPhotoContentType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}
