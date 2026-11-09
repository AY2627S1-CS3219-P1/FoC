package outbox

import (
	"errors"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func encodeDeleted(fact DeletedFact) (proto.Message, error) {
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
		DeletionRequestId: fact.DeletionRequestID.String(),
		DeletedAt:         deletedAt,
	}, nil
}
