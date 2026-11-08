package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
)

// UserDeletedTopic, as provisioned in infra/kafka/topics.conf.
const UserDeletedTopic = "users.user_deleted"

// Recorder lists the deletion-marker operation the users.user_deleted handler
// calls.
type Recorder interface {
	RecordDeletion(ctx context.Context, userID, eventID string, deletedAt time.Time) error
}

// UserDeletions records committed account deletions, so a delayed users.create
// or a lazy account open cannot re-provision the deleted user or grant its
// starting credits again.
type UserDeletions struct {
	Recorder Recorder
}

// Handle records one users.user_deleted fact. The marker insert is
// idempotent, so a redelivered event changes nothing. A fact the handler
// cannot interpret is acknowledged without recording: redelivery cannot fix
// it, and blocking the partition would stall every later deletion.
func (h *UserDeletions) Handle(ctx context.Context, meta kafka.Metadata, event *userseventsv1.UserDeleted) error {
	log := slog.With(
		"topic", meta.Topic,
		"partition", meta.Partition,
		"offset", meta.Offset,
		"event_id", event.GetEventId(),
		"user_id", event.GetUserId(),
	)

	userID, eventID, err := deletionIDs(event)
	if err != nil {
		log.ErrorContext(
			ctx,
			"User deleted event has invalid identifiers; acknowledged without recording",
			"error",
			err,
		)
		return nil
	}

	// The broker key is the canonical user UUID; a mismatch means the fact
	// does not describe its key and must not retire that user's marker space.
	key, keyErr := uuid.Parse(string(meta.Key))
	if keyErr != nil || key != userID {
		log.ErrorContext(ctx, "User deleted event key does not match its user; acknowledged without recording")
		return nil
	}

	deletedAt := time.Now().UTC()
	if event.GetDeletedAt() != nil {
		deletedAt = event.GetDeletedAt().AsTime()
	}

	if err := h.Recorder.RecordDeletion(ctx, userID.String(), eventID.String(), deletedAt); err != nil {
		return err
	}

	log.InfoContext(ctx, "User deletion recorded")
	return nil
}

// deletionIDs parses the event's stable identifiers.
func deletionIDs(event *userseventsv1.UserDeleted) (uuid.UUID, uuid.UUID, error) {
	userID, err := uuid.Parse(event.GetUserId())
	if err != nil || userID == uuid.Nil {
		return uuid.Nil, uuid.Nil, errInvalidUserID
	}
	eventID, err := uuid.Parse(event.GetEventId())
	if err != nil || eventID == uuid.Nil {
		return uuid.Nil, uuid.Nil, errInvalidEventID
	}
	return userID, eventID, nil
}
