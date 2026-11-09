package store

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserDeletedOutbox struct{ db *gorm.DB }

func NewUserDeletedOutbox(db *gorm.DB) *UserDeletedOutbox {
	return &UserDeletedOutbox{db: db}
}

func (s *UserDeletedOutbox) Claim(ctx context.Context, eligibleAt, leaseUntil time.Time) (*outbox.DeletedFact, error) {
	leaseToken := uuid.New()
	var row models.UserDeletedOutbox
	result := s.db.WithContext(ctx).Raw(`
		WITH candidate AS (
			SELECT event_id
			FROM user_deleted_outbox
			WHERE published_at IS NULL
			  AND available_at <= ?
			  AND (lease_until IS NULL OR lease_until <= ?)
			ORDER BY created_at, event_id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE user_deleted_outbox AS event
		SET lease_token = ?, lease_until = ?
		FROM candidate
		WHERE event.event_id = candidate.event_id
		RETURNING event.event_id, event.user_id, event.deletion_request_id,
		          event.deleted_at, event.payload_version, event.lease_token
	`, eligibleAt, eligibleAt, leaseToken, leaseUntil).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	if row.LeaseToken == nil {
		return nil, errors.New("claimed user-deleted outbox row has no lease token")
	}
	return &outbox.DeletedFact{
		EventID:           row.EventID,
		UserID:            row.UserID,
		DeletionRequestID: row.DeletionRequestID,
		DeletedAt:         row.DeletedAt,
		PayloadVersion:    row.PayloadVersion,
		LeaseToken:        *row.LeaseToken,
	}, nil
}

func (s *UserDeletedOutbox) Published(ctx context.Context, fact outbox.DeletedFact, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&models.UserDeletedOutbox{}).
		Where("event_id = ? AND lease_token = ? AND lease_until > ? AND published_at IS NULL",
			fact.EventID, fact.LeaseToken, now).
		Updates(map[string]any{
			"published_at": now,
			"lease_token":  nil,
			"lease_until":  nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("user-deleted outbox lease lost before publication acknowledgement")
	}
	return nil
}

func (s *UserDeletedOutbox) Retry(ctx context.Context, fact outbox.DeletedFact, availableAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&models.UserDeletedOutbox{}).
		Where("event_id = ? AND lease_token = ? AND published_at IS NULL", fact.EventID, fact.LeaseToken).
		Updates(map[string]any{
			"available_at": availableAt,
			"lease_token":  nil,
			"lease_until":  nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("user-deleted outbox lease lost before retry was scheduled")
	}
	return nil
}

