package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type AuthTokens struct{ db *gorm.DB }

func NewAuthTokens(db *gorm.DB) *AuthTokens { return &AuthTokens{db: db} }

func (s *AuthTokens) Create(ctx context.Context, token *models.AuthToken) error {
	err := s.db.WithContext(ctx).Create(token).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return ErrNotFound
	}
	return err
}

func (s *AuthTokens) Consume(
	ctx context.Context,
	hash [32]byte,
	purpose models.TokenPurpose,
	now time.Time,
) (*models.AuthToken, error) {
	var token models.AuthToken
	result := s.db.WithContext(ctx).Model(&token).Clauses(clause.Returning{}).
		Where("token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?", hash[:], purpose, now).
		Updates(map[string]any{"used_at": now, "updated_at": now})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrChallengeRejected
	}
	return &token, nil
}
