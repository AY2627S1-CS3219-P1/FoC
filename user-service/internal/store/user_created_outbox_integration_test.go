package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	userdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/user"
	"github.com/google/uuid"
)

func TestUserCreatedOutboxPersistenceAndLeases(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "outbox@example.com")
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	event := userdomain.CreatedEvent{
		EventID: uuid.New(), UserID: user.ID, Email: user.Email,
		DisplayName: user.DisplayName, CreatedAt: user.CreatedAt,
	}
	if err := store.UserCreatedOutbox.Append(ctx, event); err != nil {
		t.Fatal(err)
	}

	rollbackUser := createStoreUser(t, store, "rollback-outbox@example.com")
	rollbackEvent := userdomain.CreatedEvent{
		EventID: uuid.New(), UserID: rollbackUser.ID, Email: rollbackUser.Email,
		DisplayName: rollbackUser.DisplayName, CreatedAt: rollbackUser.CreatedAt,
	}
	rollbackErr := errors.New("abort outbox transaction")
	err := store.WithTransaction(ctx, func(tx *Store) error {
		if err := tx.UserCreatedOutbox.Append(ctx, rollbackEvent); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("transaction error = %v, want rollback error", err)
	}
	var rolledBack int64
	if err := db.Model(&models.UserCreatedOutbox{}).
		Where("event_id = ?", rollbackEvent.EventID).
		Count(&rolledBack).
		Error; err != nil ||
		rolledBack != 0 {
		t.Fatalf("outbox transaction rollback left %d rows: %v", rolledBack, err)
	}

	firstLeaseUntil := now.Add(time.Minute)
	fact, err := store.UserCreatedOutbox.Claim(ctx, now, firstLeaseUntil)
	if err != nil || fact == nil {
		t.Fatalf("claim outbox row: fact=%+v err=%v", fact, err)
	}
	if fact.EventID != event.EventID || fact.UserID != event.UserID || fact.Email != event.Email ||
		fact.DisplayName != event.DisplayName || !fact.CreatedAt.Equal(event.CreatedAt.Truncate(time.Microsecond)) || fact.PayloadVersion != 1 || fact.LeaseToken == uuid.Nil {
		t.Fatalf("claimed fact = %+v, want event %+v and a lease", fact, event)
	}
	if second, err := store.UserCreatedOutbox.Claim(
		ctx,
		now,
		firstLeaseUntil.Add(time.Minute),
	); err != nil ||
		second != nil {
		t.Fatalf("claimed a row with a live lease: fact=%+v err=%v", second, err)
	}

	retryAt := now.Add(2 * time.Minute)
	if err := store.UserCreatedOutbox.Retry(ctx, *fact, retryAt); err != nil {
		t.Fatalf("schedule outbox retry: %v", err)
	}
	if early, err := store.UserCreatedOutbox.Claim(
		ctx,
		retryAt.Add(-time.Second),
		retryAt.Add(time.Minute),
	); err != nil ||
		early != nil {
		t.Fatalf("claimed before retry time: fact=%+v err=%v", early, err)
	}
	retried, err := store.UserCreatedOutbox.Claim(ctx, retryAt, retryAt.Add(time.Minute))
	if err != nil || retried == nil {
		t.Fatalf("claim retried outbox row: fact=%+v err=%v", retried, err)
	}
	if retried.EventID != event.EventID || retried.UserID != event.UserID || retried.LeaseToken == fact.LeaseToken {
		t.Fatalf("retry changed event identity or reused lease: first=%+v retry=%+v", fact, retried)
	}
	if err := store.UserCreatedOutbox.Published(ctx, *fact, retryAt); err == nil {
		t.Fatal("stale lease acknowledged publication")
	}
	if err := store.UserCreatedOutbox.Published(ctx, *retried, retryAt.Add(time.Second)); err != nil {
		t.Fatalf("acknowledge publication: %v", err)
	}
	if pending, err := store.UserCreatedOutbox.Claim(
		ctx,
		retryAt.Add(2*time.Second),
		retryAt.Add(3*time.Minute),
	); err != nil ||
		pending != nil {
		t.Fatalf("published row was claimed again: fact=%+v err=%v", pending, err)
	}
	var published models.UserCreatedOutbox
	if err := db.Take(&published, "event_id = ?", event.EventID).Error; err != nil {
		t.Fatal(err)
	}
	if published.PublishedAt == nil || published.LeaseToken != nil || published.LeaseUntil != nil {
		t.Fatalf("published row retained pending state: %+v", published)
	}
}
