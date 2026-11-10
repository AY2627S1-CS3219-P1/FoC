package order

import (
	"context"

	"github.com/google/uuid"
)

// ParticipantReader reads the current persisted assignment, without public projections.
type ParticipantReader interface {
	Get(context.Context, string) (Order, error)
}

type Participants struct{ reader ParticipantReader }

func NewParticipants(reader ParticipantReader) *Participants { return &Participants{reader: reader} }

// GetOrderParticipants is an internal read. Historical couriers never become current participants.
func (s *Participants) GetOrderParticipants(ctx context.Context, id string) (Order, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return Order{}, ErrInvalid
	}
	return s.reader.Get(ctx, parsed.String())
}
