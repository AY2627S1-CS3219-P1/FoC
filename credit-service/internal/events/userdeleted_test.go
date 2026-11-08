package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	deletedUserID  = "44444444-4444-4444-8444-444444444444"
	deletedEventID = "55555555-5555-4555-8555-555555555555"
)

type fakeRecorder struct {
	calls []string
	err   error
}

func (f *fakeRecorder) RecordDeletion(_ context.Context, userID, eventID string, _ time.Time) error {
	f.calls = append(f.calls, userID+" "+eventID)
	return f.err
}

func deletedEvent() *userseventsv1.UserDeleted {
	return &userseventsv1.UserDeleted{
		EventId:   deletedEventID,
		UserId:    deletedUserID,
		DeletedAt: timestamppb.New(time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)),
	}
}

func TestUserDeletionsRecordsValidFacts(t *testing.T) {
	recorder := &fakeRecorder{}
	h := &events.UserDeletions{Recorder: recorder}

	err := h.Handle(context.Background(), kafka.Metadata{
		Topic: events.UserDeletedTopic, Key: []byte(deletedUserID),
	}, deletedEvent())
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != deletedUserID+" "+deletedEventID {
		t.Fatalf("calls %v", recorder.calls)
	}
}

// A fact the handler cannot interpret must not retire a marker space: it is
// acknowledged without recording, because redelivery cannot fix it.
func TestUserDeletionsAcknowledgeMalformedFacts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		event *userseventsv1.UserDeleted
	}{
		{"invalid user id", deletedUserID, &userseventsv1.UserDeleted{EventId: deletedEventID, UserId: "nope"}},
		{"invalid event id", deletedUserID, &userseventsv1.UserDeleted{EventId: "nope", UserId: deletedUserID}},
		{"key mismatch", "66666666-6666-4666-8666-666666666666", deletedEvent()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &fakeRecorder{}
			h := &events.UserDeletions{Recorder: recorder}
			if err := h.Handle(context.Background(), kafka.Metadata{
				Topic: events.UserDeletedTopic, Key: []byte(tc.key),
			}, tc.event); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(recorder.calls) != 0 {
				t.Fatalf("%s recorded: %v", tc.name, recorder.calls)
			}
		})
	}
}

func TestUserDeletionsFailClosedOnRecorderErrors(t *testing.T) {
	recorder := &fakeRecorder{err: errors.New("database down")}
	h := &events.UserDeletions{Recorder: recorder}

	err := h.Handle(context.Background(), kafka.Metadata{
		Topic: events.UserDeletedTopic, Key: []byte(deletedUserID),
	}, deletedEvent())
	if err == nil {
		t.Fatal("recorder failure acknowledged")
	}
}
