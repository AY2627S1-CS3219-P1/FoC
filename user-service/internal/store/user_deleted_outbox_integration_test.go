package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDeletedOutboxLeaseRetryKeepsFactAfterUserIsGone(t *testing.T) {
	ctx := context.Background()
	persistence, db := setupAuthStore(t)
	fact := deletionFact(uuid.New()) // The fact deliberately has no live-user FK.
	if err := db.Create(&fact).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	first, err := persistence.UserDeletedOutbox.Claim(ctx, now, now.Add(time.Minute))
	if err != nil || first == nil {
		t.Fatalf("claim: %+v %v", first, err)
	}
	if next, err := persistence.UserDeletedOutbox.Claim(ctx, now, now.Add(time.Minute)); err != nil || next != nil {
		t.Fatalf("claimed live lease: %+v %v", next, err)
	}
	retryAt := now.Add(2 * time.Minute)
	if err := persistence.UserDeletedOutbox.Retry(ctx, *first, retryAt); err != nil {
		t.Fatal(err)
	}
	if early, err := persistence.UserDeletedOutbox.Claim(ctx, now, now.Add(time.Minute)); err != nil || early != nil {
		t.Fatalf("retry became eligible too early: %+v %v", early, err)
	}
	second, err := persistence.UserDeletedOutbox.Claim(ctx, retryAt, retryAt.Add(time.Minute))
	if err != nil || second == nil {
		t.Fatalf("reclaim: %+v %v", second, err)
	}
	if second.EventID != first.EventID || second.UserID != first.UserID ||
		second.DeletionRequestID != first.DeletionRequestID || !second.DeletedAt.Equal(first.DeletedAt) ||
		second.LeaseToken == first.LeaseToken {
		t.Fatal("retry changed fact identity or reused stale lease")
	}
	if err := persistence.UserDeletedOutbox.Published(ctx, *first, retryAt); err == nil {
		t.Fatal("stale publisher acknowledged the new lease")
	}
	if err := persistence.UserDeletedOutbox.Published(ctx, *second, retryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pending, err := persistence.UserDeletedOutbox.Claim(
		ctx,
		retryAt.Add(time.Minute),
		retryAt.Add(2*time.Minute),
	); err != nil ||
		pending != nil {
		t.Fatalf("acknowledged fact was reclaimed: %+v %v", pending, err)
	}
}
