package order

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/google/uuid"
)

const MaxImageBytes int64 = 10000000
const ImageUploadLifetime = 10 * time.Minute

type ItemImageUpload struct {
	ID, OwnerID, Key, ContentType, State string
	SizeBytes                            int64
	CreatedAt, UploadExpiresAt           time.Time
}

type ItemImageView struct{ ID, DownloadURL string }

type ItemImageRepository interface {
	SaveItemImage(context.Context, ItemImageUpload) error
	ItemImage(context.Context, string, string) (ItemImageUpload, error)
	OrderImages(context.Context, string) ([]ItemImageUpload, error)
	ClaimOrphanImages(context.Context, time.Time, time.Time) ([]ItemImageUpload, error)
	DeleteOrphanImage(context.Context, string) error
}

type ItemImages struct {
	repository ItemImageRepository
	storage    images.ObjectStorage
	now        func() time.Time
}

func NewItemImages(repository ItemImageRepository, storage images.ObjectStorage) *ItemImages {
	return &ItemImages{repository: repository, storage: storage, now: time.Now}
}

func (s *ItemImages) CreateUpload(ctx context.Context, caller Caller, contentType string, size int64) (ItemImageUpload, images.UploadGrant, error) {
	if caller.ID == "" || caller.Suspended {
		return ItemImageUpload{}, images.UploadGrant{}, ErrForbidden
	}
	owner, err := parseID(caller.ID)
	if err != nil {
		return ItemImageUpload{}, images.UploadGrant{}, ErrForbidden
	}
	if size < 1 || size > MaxImageBytes || !validImageType(contentType) {
		return ItemImageUpload{}, images.UploadGrant{}, ErrInvalid
	}
	if s.storage == nil {
		return ItemImageUpload{}, images.UploadGrant{}, ErrUnavailable
	}
	now := s.now().UTC()
	id := uuid.NewString()
	image := ItemImageUpload{ID: id, OwnerID: owner.String(), Key: "order/item/" + id, ContentType: contentType, SizeBytes: size, State: "uploading", CreatedAt: now, UploadExpiresAt: now.Add(ImageUploadLifetime)}
	if err := s.repository.SaveItemImage(ctx, image); err != nil {
		return ItemImageUpload{}, images.UploadGrant{}, err
	}
	grant, err := s.storage.CreateUpload(ctx, images.UploadRequest{Key: image.Key, ContentType: contentType, SizeBytes: size})
	if err != nil {
		return ItemImageUpload{}, images.UploadGrant{}, ErrUnavailable
	}
	return image, grant, nil
}

func validImageType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}

// Prepare inspects immutable objects before entering the admission transaction.
// Attachment rechecks ownership/state atomically; cleanup may win in between.
func (s *ItemImages) Prepare(ctx context.Context, caller Caller, ids []string) ([]ItemImageUpload, error) {
	if caller.ID == "" || caller.Suspended {
		return nil, ErrForbidden
	}
	if len(ids) > 5 {
		return nil, ErrInvalid
	}
	if s.storage == nil {
		return nil, ErrUnavailable
	}
	result := make([]ItemImageUpload, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		parsed, err := parseID(id)
		if err != nil || seen[parsed.String()] {
			return nil, ErrInvalid
		}
		id = parsed.String()
		seen[id] = true
		item, err := s.repository.ItemImage(ctx, caller.ID, id)
		if err != nil {
			return nil, err
		}
		if item.State != "uploading" {
			return nil, ErrPrecondition
		}
		info, err := s.storage.Inspect(ctx, item.Key)
		if errors.Is(err, images.ErrObjectNotFound) {
			return nil, ErrPrecondition
		}
		if err != nil {
			return nil, ErrUnavailable
		}
		if !info.Exists || info.Size != item.SizeBytes || info.Size < 1 || info.Size > MaxImageBytes || info.ContentType != item.ContentType {
			return nil, ErrPrecondition
		}
		result = append(result, item)
	}
	return result, nil
}

// ForOrder accepts only the already-authorized Order, then checks the same
// visibility boundary before issuing temporary object credentials.
func (s *ItemImages) ForOrder(ctx context.Context, caller Caller, o Order) ([]ItemImageView, error) {
	if caller.ID == "" {
		return nil, ErrForbidden
	}
	if !caller.Admin && !o.HasParticipant(caller.ID) && (caller.Suspended || o.Status != AwaitingCourier || !o.ExpiresAt.After(s.now())) {
		return nil, ErrNotFound
	}
	rows, err := s.repository.OrderImages(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	result := make([]ItemImageView, 0, len(rows))
	for _, row := range rows {
		if s.storage == nil {
			return nil, ErrUnavailable
		}
		grant, err := s.storage.CreateDownload(ctx, row.Key)
		if err != nil {
			return nil, ErrUnavailable
		}
		result = append(result, ItemImageView{ID: row.ID, DownloadURL: grant.URL})
	}
	return result, nil
}

func (s *ItemImages) CleanupOnce(ctx context.Context) error {
	if s.storage == nil {
		return nil
	}
	now := s.now().UTC()
	rows, err := s.repository.ClaimOrphanImages(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.storage.Delete(ctx, row.Key); err != nil {
			return err
		}
		if err := s.repository.DeleteOrphanImage(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}
