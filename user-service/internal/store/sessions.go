package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type Sessions struct{ db *gorm.DB }

func NewSessions(db *gorm.DB) *Sessions { return &Sessions{db: db} }

func (s *Sessions) Create(ctx context.Context, session *models.Session) error {
	err := s.db.WithContext(ctx).Create(session).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return ErrNotFound
	}
	return err
}

func (s *Sessions) UpdateTokenHash(ctx context.Context, id uint, tokenHash []byte) error {
	result := s.db.WithContext(ctx).Model(&models.Session{}).
		Where("id = ?", id).Update("token_hash", tokenHash)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Sessions) Revoke(
	ctx context.Context,
	id uint,
	hash [32]byte,
	now time.Time,
) error {
	result := s.db.WithContext(ctx).Model(&models.Session{}).
		Where("id = ? AND token_hash = ? AND revoked_at IS NULL AND expires_at > ?", id, hash[:], now).
		Update("revoked_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSessionRejected
	}
	return nil
}
