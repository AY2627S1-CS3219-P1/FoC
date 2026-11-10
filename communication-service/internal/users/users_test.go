package users

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	usereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeStore map[uuid.UUID]string

func (f fakeStore) SaveEmail(_ context.Context, userID uuid.UUID, email string, _ time.Time) error {
	f[userID] = email
	return nil
}

func TestProjectionSavesValidEmailsAndSkipsMalformedEvents(t *testing.T) {
	id := uuid.New()
	cases := map[string]struct {
		event *usereventsv1.UserCreated
		saved bool
	}{
		"valid":           {&usereventsv1.UserCreated{UserId: id.String(), Email: "alice@foc.local"}, true},
		"invalid user ID": {&usereventsv1.UserCreated{UserId: "nope", Email: "alice@foc.local"}, false},
		"invalid email":   {&usereventsv1.UserCreated{UserId: id.String(), Email: "Alice <alice@foc.local>"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := fakeStore{}
			p := &Projection{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			tc.event.CreatedAt = timestamppb.Now()
			if err := p.Handle(context.Background(), kafka.Metadata{}, tc.event); err != nil {
				t.Fatal(err)
			}
			if _, ok := store[id]; ok != tc.saved {
				t.Fatalf("saved = %v", store)
			}
		})
	}
}
