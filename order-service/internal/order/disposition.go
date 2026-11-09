package order

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Disposition records courier-only final item location without another outcome.
type Disposition struct {
	mutator *Mutator
	now     func() time.Time
	newID   func() string
}

func NewDisposition(mutator *Mutator) *Disposition {
	return &Disposition{mutator: mutator, now: time.Now, newID: uuid.NewString}
}

// RecordItemLocation amends a picked-up Cancelled/Failed order without another
// outcome. NewChange authorizes this same-status amendment, not Transition.
// Identical normalized submissions are repeats; corrections retain audit history.
func (s *Disposition) RecordItemLocation(ctx context.Context, c Caller, orderID, location string) (Order, error) {
	if c.ID == "" {
		return Order{}, ErrForbidden
	}
	id, err := parseID(orderID)
	if err != nil {
		return Order{}, err
	}
	location = strings.TrimSpace(location)
	if !utf8.ValidString(location) || utf8.RuneCountInString(location) < 1 || utf8.RuneCountInString(location) > 500 {
		return Order{}, ErrInvalid
	}

	var result Order
	err = s.mutator.Mutate(ctx, id.String(), func(current Order, _ []Offer) (*Change, error) {
		if !current.HasParticipant(c.ID) {
			return nil, ErrNotFound
		}
		if !current.IsAssignedTo(c.ID) {
			return nil, ErrForbidden
		}
		if current.PickedUpAt == nil || (current.Status != Cancelled && current.Status != Failed) {
			return nil, ErrPrecondition
		}
		if current.ItemLocation != nil && *current.ItemLocation == location {
			result = current
			return nil, nil
		}

		after := cloneOrder(current)
		after.ItemLocation = &location
		after.UpdatedAt = s.now().UTC().Truncate(time.Microsecond)
		after.Revision++
		details, err := json.Marshal(struct {
			ItemLocation string `json:"item_location"`
		}{location})
		if err != nil {
			return nil, err
		}
		change, err := NewChange(&current, after, Actor{Kind: User, ID: c.ID}, RecordItemLocation, details, s.newID)
		if err != nil {
			return nil, err
		}
		result = after
		return &change, nil
	})
	if err != nil {
		return Order{}, err
	}
	return result, nil
}
