// Package users keeps a local copy of each user's email from users.create, so
// notifications can reach users without Telegram.
package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	usereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Topic carries UserCreated events.
const Topic = "users.create"

// ErrNotFound means no users.create event has been applied for the user.
var ErrNotFound = errors.New("user email not found")

// Store saves and reads the local copies.
type Store interface {
	SaveEmail(ctx context.Context, userID uuid.UUID, email string, createdAt time.Time) error
}

// Projection applies users.create events to Store.
type Projection struct {
	Store Store
	Log   *slog.Logger
}

// Handle implements a pkg/kafka handler for Topic. Malformed events are logged
// and skipped, since replaying can't fix them; storage errors are replayed.
func (p *Projection) Handle(ctx context.Context, _ kafka.Metadata, e *usereventsv1.UserCreated) error {
	userID, err := uuid.Parse(e.GetUserId())
	if err != nil {
		p.Log.ErrorContext(ctx, "skipping user event with an invalid user ID", "event_id", e.GetEventId())
		return nil
	}
	address, err := mail.ParseAddress(e.GetEmail())
	if err != nil || address.Address != e.GetEmail() {
		p.Log.ErrorContext(ctx, "skipping user event with an invalid email", "event_id", e.GetEventId())
		return nil
	}
	return p.Store.SaveEmail(ctx, userID, e.GetEmail(), e.GetCreatedAt().AsTime())
}

// PostgresStore implements Store with the table from migration 00005.
type PostgresStore struct {
	Queries *db.Queries
}

var _ Store = (*PostgresStore)(nil)

// SaveEmail implements Store. Saving an existing user again changes nothing.
func (s *PostgresStore) SaveEmail(ctx context.Context, userID uuid.UUID, email string, createdAt time.Time) error {
	if err := s.Queries.SaveUserEmail(ctx, db.SaveUserEmailParams{
		UserID: userID, Email: email, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("save user email: %w", err)
	}
	return nil
}

// Email returns the user's email, or ErrNotFound.
func (s *PostgresStore) Email(ctx context.Context, userID uuid.UUID) (string, error) {
	email, err := s.Queries.GetUserEmail(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get user email: %w", err)
	}
	return email, nil
}
