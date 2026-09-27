package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type Sessions struct{ db *gorm.DB }

func NewSessions(db *gorm.DB) *Sessions { return &Sessions{db: db} }

func (s *Sessions) CreateSession(ctx context.Context, session *models.Session) error {
	err := s.db.WithContext(ctx).Create(session).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return ErrNotFound
	}
	return err
}

func (s *Sessions) RevokeSession(
	ctx context.Context,
	id uuid.UUID,
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
