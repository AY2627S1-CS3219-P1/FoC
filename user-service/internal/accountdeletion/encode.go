package accountdeletion

import (
	"errors"
	"time"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	deletedTopic = "users.user_deleted"

	defaultDrainLimit = 50
	claimLease        = 30 * time.Second
	publishTimeout    = 5 * time.Second
	retryDelay        = 2 * time.Second
	// rpcDeadline bounds one drain call so a slow or hung Order/Credit service
	// reads as "unknown", never as "empty".
	rpcDeadline = 5 * time.Second
	// maxBackoff caps how long the worker waits between drain attempts after a
	// transient dependency failure.
	maxBackoff = 5 * time.Minute
)

// encodeDeleted builds the Kafka UserDeleted message from a committed fact.
func encodeDeleted(fact deletiondb.ClaimCommittedFactRow) (proto.Message, error) {
	if fact.PayloadVersion != 1 {
		return nil, errors.New("unsupported user-deleted outbox payload version")
	}
	if fact.EventID == uuid.Nil || fact.UserID == uuid.Nil || fact.DeletedAt.IsZero() ||
		fact.DeletionRequestID == uuid.Nil {
		return nil, errors.New("invalid user-deleted outbox fact")
	}
	deletedAt := timestamppb.New(fact.DeletedAt)
	if err := deletedAt.CheckValid(); err != nil {
		return nil, err
	}
	return &events.UserDeleted{
		EventId:           fact.EventID.String(),
		UserId:            fact.UserID.String(),
		DeletedAt:         deletedAt,
		DeletionRequestId: fact.DeletionRequestID.String(),
	}, nil
}
