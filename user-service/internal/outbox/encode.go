package outbox

import (
	"errors"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func encode(fact Fact) (proto.Message, error) {
	if fact.PayloadVersion != 1 {
		return nil, errors.New("unsupported user-created outbox payload version")
	}
	if fact.EventID == uuid.Nil || fact.UserID == uuid.Nil || fact.CreatedAt.IsZero() ||
		fact.Email == "" || fact.DisplayName == "" {
		return nil, errors.New("invalid user-created outbox fact")
	}
	createdAt := timestamppb.New(fact.CreatedAt)
	if err := createdAt.CheckValid(); err != nil {
		return nil, err
	}
	return &events.UserCreated{
		EventId:     fact.EventID.String(),
		UserId:      fact.UserID.String(),
		Email:       fact.Email,
		DisplayName: fact.DisplayName,
		CreatedAt:   createdAt,
	}, nil
}
