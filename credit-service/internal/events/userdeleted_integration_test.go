//go:build integration

package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/testsupport/postgres"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
)

// A users.create that arrives after the committed deletion must grant nothing,
// across replays and in either topic order.
func TestUserCreatedAfterUserDeletedGrantsNothing(t *testing.T) {
	pool := postgres.Setup(t)
	ctx := context.Background()
	store := account.NewPostgresStore(pool)
	accounts := account.NewService(store, store, store, time.Now)
	deletions := &events.UserDeletions{Recorder: accounts}
	users := &events.Users{Opener: accounts}
	user := uuid.New()

	if err := deletions.Handle(ctx, kafka.Metadata{
		Topic: events.UserDeletedTopic, Key: []byte(user.String()),
	}, &userseventsv1.UserDeleted{EventId: uuid.NewString(), UserId: user.String()}); err != nil {
		t.Fatal(err)
	}

	created := &userseventsv1.UserCreated{EventId: uuid.NewString(), UserId: user.String()}
	meta := kafka.Metadata{Topic: events.UserCreatedTopic, Key: []byte(user.String())}
	for range 2 {
		if err := users.Handle(ctx, meta, created); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := accounts.GetAccount(ctx, user.String()); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("late users.create resurrected the account: %v", err)
	}

	// The create fact still works for a user with no deletion marker.
	other := uuid.New()
	if err := users.Handle(ctx, kafka.Metadata{
		Topic: events.UserCreatedTopic, Key: []byte(other.String()),
	}, &userseventsv1.UserCreated{EventId: uuid.NewString(), UserId: other.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.GetAccount(ctx, other.String()); err != nil {
		t.Fatalf("normal provisioning broken: %v", err)
	}
}
