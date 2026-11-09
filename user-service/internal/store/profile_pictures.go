// Profile picture persistence; the row ID is the reference, object_key is metadata.

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

func (s *ProfilePictures) InsertPending(ctx context.Context, picture *models.ProfilePicture) error {
	return s.db.WithContext(ctx).Create(picture).Error
}

// GetPending returns a pending row owned by the caller.
func (s *ProfilePictures) GetPending(
	ctx context.Context,
	userID, id uuid.UUID,
) (*models.ProfilePicture, error) {
	var picture models.ProfilePicture
	err := s.db.WithContext(ctx).
		Take(&picture, "id = ? AND user_id = ? AND state = ?", id, userID, models.ProfilePicturePending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPictureNotFound
	}
	if err != nil {
		return nil, err
	}
	return &picture, nil
}

func (s *ProfilePictures) GetPictureByID(ctx context.Context, id uuid.UUID) (*models.ProfilePicture, error) {
	var picture models.ProfilePicture
	err := s.db.WithContext(ctx).Take(&picture, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPictureNotFound
	}
	if err != nil {
		return nil, err
	}
	return &picture, nil
}

// Activate retires the previous picture and points the user at this one; returns the old key.
func (s *ProfilePictures) Activate(
	ctx context.Context,
	userID, pictureID, updatedBy uuid.UUID,
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

		var pending models.ProfilePicture
		err = tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&pending, "id = ? AND user_id = ? AND state = ?",
				pictureID, userID, models.ProfilePicturePending).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPictureNotFound
		}
		if err != nil {
			return err
		}

		if user.ProfilePictureID != nil {
			var old models.ProfilePicture
			err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
				Take(&old, "id = ? AND state = ?", *user.ProfilePictureID, models.ProfilePictureActive).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				// A stale pointer should not block a replacement.
			case err != nil:
				return err
			default:
				oldKey = &old.ObjectKey
				if err := tx.WithContext(ctx).Model(&models.ProfilePicture{}).
					Where("id = ? AND state = ?", old.ID, models.ProfilePictureActive).
					Update("state", models.ProfilePictureDeleting).Error; err != nil {
					return err
				}
			}
		}

		res := tx.WithContext(ctx).Model(&models.ProfilePicture{}).
			Where("id = ? AND state = ?", pending.ID, models.ProfilePicturePending).
			Update("state", models.ProfilePictureActive)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrPictureNotFound
		}

		res = tx.WithContext(ctx).Model(&models.User{}).
			Where("id = ? AND role <> ?", userID, models.RoleSuspended).
			Updates(map[string]any{
				"profile_picture_id": pending.ID,
				"updated_by":         updatedBy,
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

// Deactivate retires the current row and clears the pointer; idempotent.
func (s *ProfilePictures) Deactivate(
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
		if user.ProfilePictureID == nil {
			return nil
		}
		var current models.ProfilePicture
		err = tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&current, "id = ? AND state = ?", *user.ProfilePictureID, models.ProfilePictureActive).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// Stale pointer: clear it below.
		case err != nil:
			return err
		default:
			oldKey = &current.ObjectKey
			if err := tx.WithContext(ctx).Model(&models.ProfilePicture{}).
				Where("id = ? AND state = ?", current.ID, models.ProfilePictureActive).
				Update("state", models.ProfilePictureDeleting).Error; err != nil {
				return err
			}
		}
		return tx.WithContext(ctx).Model(&models.User{}).
			Where("id = ? AND role <> ?", userID, models.RoleSuspended).
			Updates(map[string]any{
				"profile_picture_id": nil,
				"updated_by":         updatedBy,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return oldKey, nil
}

// ClaimExpiredPending moves abandoned pending rows to deleting.
func (s *ProfilePictures) ClaimExpiredPending(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]models.ProfilePicture, error) {
	if limit <= 0 {
		limit = 50
	}
	rows := make([]models.ProfilePicture, 0, limit)
	err := s.db.WithContext(ctx).Raw(`
UPDATE profile_pictures AS target
SET state = 'deleting'
WHERE target.id IN (
    SELECT candidate.id
    FROM profile_pictures AS candidate
    WHERE candidate.state = 'pending' AND candidate.created_at <= ?
    ORDER BY candidate.created_at, candidate.id
    LIMIT ?
    FOR UPDATE SKIP LOCKED
)
RETURNING target.id, target.user_id, target.object_key, target.content_type,
          target.size_bytes, target.state, target.created_at
`, before, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ListDeleting returns rows awaiting object removal; deletion is idempotent.
func (s *ProfilePictures) ListDeleting(
	ctx context.Context,
	dueAt time.Time,
	limit int,
) ([]models.ProfilePicture, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []models.ProfilePicture
	err := s.db.WithContext(ctx).
		Where("state = ? AND (delete_retry_at IS NULL OR delete_retry_at <= ?)",
			models.ProfilePictureDeleting, dueAt).
		Order("created_at, id").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// ScheduleDeleteRetry defers a failed object deletion without removing its row.
func (s *ProfilePictures) ScheduleDeleteRetry(ctx context.Context, id uuid.UUID, retryAt time.Time) error {
	return s.db.WithContext(ctx).Model(&models.ProfilePicture{}).
		Where("id = ? AND state = ?", id, models.ProfilePictureDeleting).
		Update("delete_retry_at", retryAt).Error
}

// DeletePicture removes a row after its object is gone.
func (s *ProfilePictures) DeletePicture(ctx context.Context, id uuid.UUID) error {
	return s.db.WithContext(ctx).
		Where("id = ? AND state = ?", id, models.ProfilePictureDeleting).
		Delete(&models.ProfilePicture{}).Error
}
