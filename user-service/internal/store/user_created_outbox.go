package store

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	userdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/user"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserCreatedOutbox struct{ db *gorm.DB }

func NewUserCreatedOutbox(db *gorm.DB) *UserCreatedOutbox {
	return &UserCreatedOutbox{db: db}
}

func (s *UserCreatedOutbox) Append(ctx context.Context, event userdomain.CreatedEvent) error {
	row := models.UserCreatedOutbox{
		EventID:        event.EventID,
		UserID:         event.UserID,
		Email:          event.Email,
		DisplayName:    event.DisplayName,
		UserCreatedAt:  event.CreatedAt,
		PayloadVersion: 1,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *UserCreatedOutbox) Claim(ctx context.Context, eligibleAt, leaseUntil time.Time) (*outbox.Fact, error) {
	leaseToken := uuid.New()
	var row models.UserCreatedOutbox
	result := s.db.WithContext(ctx).Raw(`
		WITH candidate AS (
			SELECT event_id
			FROM user_created_outbox
			WHERE published_at IS NULL
			  AND available_at <= ?
			  AND (lease_until IS NULL OR lease_until <= ?)
			ORDER BY created_at, event_id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE user_created_outbox AS event
		SET lease_token = ?, lease_until = ?
		FROM candidate
		WHERE event.event_id = candidate.event_id
		RETURNING event.event_id, event.user_id, event.email, event.display_name,
		          event.user_created_at, event.payload_version, event.lease_token
	`, eligibleAt, eligibleAt, leaseToken, leaseUntil).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	if row.LeaseToken == nil {
		return nil, errors.New("claimed user-created outbox row has no lease token")
	}
	return &outbox.Fact{
		EventID:        row.EventID,
		UserID:         row.UserID,
		Email:          row.Email,
		DisplayName:    row.DisplayName,
		CreatedAt:      row.UserCreatedAt,
		PayloadVersion: row.PayloadVersion,
		LeaseToken:     *row.LeaseToken,
	}, nil
}

func (s *UserCreatedOutbox) Published(ctx context.Context, fact outbox.Fact, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&models.UserCreatedOutbox{}).
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
		return errors.New("user-created outbox lease lost before publication acknowledgement")
	}
	return nil
}

func (s *UserCreatedOutbox) Retry(ctx context.Context, fact outbox.Fact, availableAt time.Time) error {
	result := s.db.WithContext(ctx).Model(&models.UserCreatedOutbox{}).
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
		return errors.New("user-created outbox lease lost before retry was scheduled")
	}
	return nil
}
