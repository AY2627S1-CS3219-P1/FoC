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

func (s *Sessions) UpdateTokenHash(ctx context.Context, id uuid.UUID, tokenHash []byte) error {
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

// RevokeAllForUser revokes every unrevoked session of userID and returns their
// IDs so the caller can record each session's access tokens in the revocation
// dictionary. Expired sessions are included; revoking them is harmless.
//
// The revoke and the ID list come from one UPDATE … RETURNING, so a session
// committed by a concurrent login/refresh cannot be revoked without appearing
// in the returned IDs (which would leave its access token valid until expiry).
func (s *Sessions) RevokeAllForUser(
	ctx context.Context,
	userID uuid.UUID,
	now time.Time,
) ([]uuid.UUID, error) {
	var sessionIDs []uuid.UUID
	err := s.db.WithContext(ctx).Raw(
		`UPDATE sessions SET revoked_at = ?, updated_at = ?
		 WHERE user_id = ? AND revoked_at IS NULL
		 RETURNING id`,
		now, now, userID,
	).Scan(&sessionIDs).Error
	if err != nil {
		return nil, err
	}
	return sessionIDs, nil
}

// RevokedSince lists sessions whose revocation happened after since, so the
// reconciliation sweep can republish revocations whose dictionary write
// failed or was lost to a crash between the session commit and publication.
// The list is a superset: republishing a session that is already recorded is
// an idempotent upsert.
func (s *Sessions) RevokedSince(ctx context.Context, since time.Time) ([]uuid.UUID, error) {
	var sessionIDs []uuid.UUID
	err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM sessions WHERE revoked_at IS NOT NULL AND revoked_at > ?`,
		since,
	).Scan(&sessionIDs).Error
	if err != nil {
		return nil, err
	}
	return sessionIDs, nil
}
