package evidence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/google/uuid"
)

const (
	// Repository-wide image convention: five types, 10MB decimal cap.
	maxImageBytes = 10000000
	// Unattached staging rows are reaped after this grace period.
	orphanAfter = 24 * time.Hour

	keyPrefix          = "evidence/"
	stateUploading     = "uploading"
	contentTypeMessage = "content type must be image/jpeg, image/png, image/webp, image/heic or image/heif"
)

// Storage signs grants, inspects stored metadata and deletes objects.
type Storage interface {
	CreateUpload(context.Context, images.UploadRequest) (images.UploadGrant, error)
	Inspect(context.Context, string) (images.ObjectInfo, error)
	CreateDownload(context.Context, string) (images.DownloadGrant, error)
	Delete(context.Context, string) error
}

// Service stages, checks and signs evidence images. storage is nil when the
// deployment has no object storage: uploads are then unavailable, and
// submissions without images still work.
type Service struct {
	repo    Repository
	storage Storage
}

func NewService(repo Repository, storage Storage) *Service {
	return &Service{repo: repo, storage: storage}
}

// CreateUpload signs a direct upload, then stages the row with the grant's
// shape, so a signing failure leaves no row behind.
func (s *Service) CreateUpload(
	ctx context.Context,
	ownerID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (uuid.UUID, images.UploadGrant, error) {
	if s.storage == nil {
		return uuid.Nil, images.UploadGrant{}, unavailable(errStorageNotEnabled)
	}
	if !validContentType(contentType) {
		return uuid.Nil, images.UploadGrant{}, invalid(errEvidenceInvalid, contentTypeMessage)
	}
	if sizeBytes < 1 || sizeBytes > maxImageBytes {
		return uuid.Nil, images.UploadGrant{}, invalid(
			errEvidenceInvalid, "image size must be between 1 and 10000000 bytes")
	}

	id, err := newID()
	if err != nil {
		return uuid.Nil, images.UploadGrant{}, err
	}
	key := keyPrefix + id.String()
	grant, err := s.storage.CreateUpload(ctx, images.UploadRequest{
		Key:         key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	})
	if err != nil {
		return uuid.Nil, images.UploadGrant{}, storageError(fmt.Errorf("sign evidence upload: %w", err))
	}

	input := StagedInput{
		ID:              id,
		OwnerID:         ownerID,
		ObjectKey:       key,
		ContentType:     contentType,
		SizeBytes:       sizeBytes,
		UploadExpiresAt: grant.ExpiresAt,
	}
	if err := s.repo.InsertStaged(ctx, input); err != nil {
		return uuid.Nil, images.UploadGrant{}, err
	}
	return id, grant, nil
}

// Prepare checks the caller's staged images against the stored objects. It
// inspects storage, so callers run it before their transaction.
func (s *Service) Prepare(ctx context.Context, ownerID uuid.UUID, ids []uuid.UUID) ([]Prepared, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.storage == nil {
		return nil, unavailable(errStorageNotEnabled)
	}
	if len(ids) > MaxPerParent {
		return nil, invalid(errTooMuchEvidence, "attach at most 5 images")
	}

	prepared := make([]Prepared, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, invalid(errEvidenceIDRequired, "image IDs must be set")
		}
		if seen[id] {
			return nil, invalid(errDuplicateEvidence, "image IDs must be unique")
		}
		seen[id] = true

		staged, err := s.repo.GetStaged(ctx, ownerID, id)
		if errors.Is(err, errEvidenceNotFound) {
			return nil, notStaged(err, id)
		}
		if err != nil {
			return nil, err
		}
		if staged.State != stateUploading {
			return nil, notReady(id, "image is no longer pending upload")
		}

		info, err := s.storage.Inspect(ctx, staged.ObjectKey)
		if errors.Is(err, images.ErrObjectNotFound) {
			return nil, notReady(id, "image upload is missing; upload it again")
		}
		if err != nil {
			return nil, storageError(fmt.Errorf("inspect evidence object: %w", err))
		}
		if !info.Exists || info.Size != staged.SizeBytes || info.ContentType != staged.ContentType {
			return nil, notReady(id, "image upload is incomplete or invalid; upload it again")
		}
		prepared = append(prepared, staged.Prepared)
	}
	return prepared, nil
}

// Views returns the attached images of each parent with fresh download URLs,
// in attachment order. Without storage it returns no images.
func (s *Service) Views(ctx context.Context, parents []Parent) (map[Parent][]View, error) {
	views := map[Parent][]View{}
	if len(parents) == 0 || s.storage == nil {
		return views, nil
	}
	attached, err := s.repo.ListForParents(ctx, parents)
	if err != nil {
		return nil, err
	}
	for _, image := range attached {
		view, err := s.sign(ctx, image.ID, image.ObjectKey, image.ContentType)
		if err != nil {
			return nil, err
		}
		views[image.Parent] = append(views[image.Parent], view)
	}
	return views, nil
}

// Locate returns the report or appeal that governs who may read an image.
func (s *Service) Locate(ctx context.Context, id uuid.UUID) (Owner, error) {
	located, err := s.repo.Locate(ctx, id)
	if errors.Is(err, errEvidenceNotFound) {
		return Owner{}, errs.WrapNotFoundError(err, "evidence not found")
	}
	if err != nil {
		return Owner{}, err
	}
	return located.Owner, nil
}

// Download signs a fresh URL for an attached image. Callers check visibility
// through Locate first.
func (s *Service) Download(ctx context.Context, id uuid.UUID) (View, error) {
	if s.storage == nil {
		return View{}, unavailable(errStorageNotEnabled)
	}
	located, err := s.repo.Locate(ctx, id)
	if errors.Is(err, errEvidenceNotFound) {
		return View{}, errs.WrapNotFoundError(err, "evidence not found")
	}
	if err != nil {
		return View{}, err
	}
	return s.sign(ctx, id, located.ObjectKey, located.ContentType)
}

// CleanupOnce claims expired staging rows, deletes their objects, then the rows.
func (s *Service) CleanupOnce(ctx context.Context) error {
	if s.storage == nil {
		return nil
	}
	now := time.Now().UTC()
	claimed, err := s.repo.ClaimOrphans(ctx, now.Add(-orphanAfter), now)
	if err != nil {
		return fmt.Errorf("claim orphan evidence: %w", err)
	}
	for _, orphan := range claimed {
		if err := s.storage.Delete(ctx, orphan.ObjectKey); err != nil {
			return fmt.Errorf("delete orphan evidence object: %w", err)
		}
		if err := s.repo.DeleteOrphan(ctx, orphan.ID); err != nil {
			return fmt.Errorf("delete orphan evidence row: %w", err)
		}
	}
	return nil
}

func (s *Service) sign(ctx context.Context, id uuid.UUID, key, contentType string) (View, error) {
	grant, err := s.storage.CreateDownload(ctx, key)
	if err != nil {
		return View{}, storageError(fmt.Errorf("sign evidence download: %w", err))
	}
	return View{ID: id, URL: grant.URL, ContentType: contentType, ExpiresAt: grant.ExpiresAt}, nil
}

func storageError(err error) *Error {
	if errors.Is(err, images.ErrStorageUnavailable) {
		return temporarilyUnavailable(err)
	}
	return unavailable(err)
}

// validContentType mirrors the migration's CHECK constraint.
func validContentType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}
