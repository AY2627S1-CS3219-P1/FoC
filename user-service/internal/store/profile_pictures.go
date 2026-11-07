// Profile picture upload persistence.

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type ProfilePictures struct{ db *gorm.DB }

func NewProfilePictures(db *gorm.DB) *ProfilePictures { return &ProfilePictures{db: db} }

func (s *ProfilePictures) InsertUpload(ctx context.Context, upload *models.ProfilePictureUpload) error {
	return s.db.WithContext(ctx).Create(upload).Error
}

// GetUpload returns a pending upload owned by the caller. Claimed rows are
// treated as absent so a submit cannot apply an object cleanup is removing.
func (s *ProfilePictures) GetUpload(
	ctx context.Context,
	userID, id uuid.UUID,
) (*models.ProfilePictureUpload, error) {
	var upload models.ProfilePictureUpload
	err := s.db.WithContext(ctx).
		Take(&upload, "id = ? AND user_id = ? AND deleting = false", id, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUploadNotFound
	}
	if err != nil {
		return nil, err
	}
	return &upload, nil
}

// SetUserPicture consumes the caller's pending upload and makes its object the
// current picture in one transaction. The returned key is the previous picture
// that the caller should delete after the transaction commits.
func (s *ProfilePictures) SetUserPicture(
	ctx context.Context,
	userID, uploadID, updatedBy uuid.UUID,
) (*string, error) {
	var oldKey *string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := NewUsers(tx).GetByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if user.IsSuspended() {
			return ErrRoleConflict
		}

		var upload models.ProfilePictureUpload
		err = tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&upload, "id = ? AND user_id = ? AND deleting = false", uploadID, userID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUploadNotFound
		}
		if err != nil {
			return err
		}

		if err := tx.WithContext(ctx).Delete(&upload).Error; err != nil {
			return err
		}

		oldKey = user.ProfilePictureKey
		res := tx.WithContext(ctx).Model(&models.User{}).
			Where("id = ? AND role <> ?", userID, models.RoleSuspended).
			Updates(map[string]any{
				"profile_picture_key": upload.ObjectKey,
				"updated_by":          updatedBy,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrRoleConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return oldKey, nil
}

// ClearUserPicture removes the current picture and returns its object key for
// best-effort deletion after the transaction commits. It is idempotent.
func (s *ProfilePictures) ClearUserPicture(
	ctx context.Context,
	userID, updatedBy uuid.UUID,
) (*string, error) {
	var oldKey *string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := NewUsers(tx).GetByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if user.IsSuspended() {
			return ErrRoleConflict
		}
		oldKey = user.ProfilePictureKey
		return tx.WithContext(ctx).Model(&models.User{}).
			Where("id = ? AND role <> ?", userID, models.RoleSuspended).
			Updates(map[string]any{
				"profile_picture_key": nil,
				"updated_by":          updatedBy,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return oldKey, nil
}

// ClaimExpiredUploads atomically marks a batch for cleanup. Including already
// claimed rows makes a failed object deletion retryable on the next pass.
func (s *ProfilePictures) ClaimExpiredUploads(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]models.ProfilePictureUpload, error) {
	if limit <= 0 {
		limit = 50
	}
	rows := make([]models.ProfilePictureUpload, 0, limit)
	err := s.db.WithContext(ctx).Raw(`
UPDATE profile_picture_uploads AS target
SET deleting = true
WHERE target.id IN (
    SELECT candidate.id
    FROM profile_picture_uploads AS candidate
    WHERE candidate.created_at <= ?
    ORDER BY candidate.created_at, candidate.id
    LIMIT ?
    FOR UPDATE SKIP LOCKED
)
RETURNING target.id, target.user_id, target.object_key, target.content_type,
          target.size_bytes, target.deleting, target.created_at
`, before, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteUploads removes claimed rows after their objects are gone. A row that
// was already deleted is ignored.
func (s *ProfilePictures) DeleteUploads(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Where("id IN ? AND deleting", ids).
		Delete(&models.ProfilePictureUpload{}).Error
}
