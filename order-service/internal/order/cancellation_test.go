package order

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCancellationInvalidInputsDoNotReachPersistence(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		name, caller, order, reason, key string
		want                             error
	}{
		{"anonymous", "", id, "", "", ErrForbidden},
		{"bad caller", "bad", id, "", "", ErrForbidden},
		{"bad order", id, "bad", "", "", ErrInvalid},
		{"zero order", id, uuid.Nil.String(), "", "", ErrInvalid},
		{"bad key", id, id, "", "bad", ErrInvalid},
		{"zero key", id, id, "", uuid.Nil.String(), ErrInvalid},
		{"long unicode reason", id, id, strings.Repeat("界", 501), "", ErrInvalid},
		{"invalid unicode", id, id, string([]byte{0xff}), "", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCanceller(
				nil,
				nil,
				AssignmentOptions{},
			).Cancel(context.Background(), Caller{ID: tc.caller}, tc.order, tc.reason, tc.key)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
