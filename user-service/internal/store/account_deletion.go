package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"gorm.io/gorm"
)

var ErrProfileImageCleanup = errors.New("profile image cleanup is not configured")

type AccountDeletion struct{ db *gorm.DB }

func NewAccountDeletion(db *gorm.DB) *AccountDeletion { return &AccountDeletion{db: db} }

// HardDeleteAccount is bound to the surrounding Store transaction. The caller
// holds the live target row lock and has rechecked current admin permissions.
func (s *AccountDeletion) HardDeleteAccount(
	ctx context.Context,
	user *models.User,
	fact models.UserDeletedOutbox,
) error {
	db := s.db.WithContext(ctx)
	var protected int64
	if err := db.Model(&models.AdminBootstrap{}).Where("user_id = ?", user.ID).Count(&protected).Error; err != nil {
		return err
	}
	if protected != 0 {
		return ErrInUse
	}
	// The profile-upload feature is not on this baseline. Never discard a
	// legacy object's sole key before its durable cleanup adapter is wired.
	if user.ProfilePictureKey != nil && *user.ProfilePictureKey != "" {
		return ErrProfileImageCleanup
	}
	if fact.UserID != user.ID || fact.PayloadVersion != 1 {
		return errors.New("invalid account-deletion fact")
	}

	// Delete email-bound registration challenges too: these may have no FK.
	// CASCADE removes sessions/tokens/favourites physically, including rows
	// that were previously soft deleted. Retained history uses SET NULL.
	if err := db.Unscoped().Where("user_id = ? OR email = ?", user.ID, user.Email).
		Delete(&models.AuthToken{}).Error; err != nil {
		return fmt.Errorf("delete account challenges: %w", err)
	}
	// Remove copied PII in both pending and acknowledged creation facts. A
	// fact already handed to a publisher cannot be recalled across topics.
	if err := db.Where("user_id = ?", user.ID).Delete(&models.UserCreatedOutbox{}).Error; err != nil {
		return fmt.Errorf("remove copied registration profile: %w", err)
	}
	result := db.Unscoped().Delete(&models.User{}, user.ID)
	if result.Error != nil {
		return fmt.Errorf("hard-delete account: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	if err := db.Create(&fact).Error; err != nil {
		return fmt.Errorf("append account deletion outbox: %w", err)
	}
	return nil
}
