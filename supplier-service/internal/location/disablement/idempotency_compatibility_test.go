package disablement_test

import (
	"context"
	"errors"

	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"

	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

func TestWorkflowCreationTimeAfterRetryLock(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	lockedAt := now.Add(2 * time.Hour)
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	repo.onRetryLock = func() { now = lockedAt }
	app := newTestService(repo, func() time.Time { return now })
	got, err := app.CreateDisablement(context.Background(), admin, disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key})
	if err != nil || !got.StartsAt.Equal(lockedAt) || !got.CreatedAt.Equal(lockedAt) {
		t.Fatalf("callback timestamp: %+v %v", got, err)
	}
	record := repo.keys[idempotency.Scope{Caller: admin.ID, Method: "CreateDisablement", Key: key}]
	if !record.ExpiresAt.Equal(lockedAt.Add(24 * time.Hour)) {
		t.Fatalf("expiry %v", record.ExpiresAt)
	}
}

func TestWorkflowRetrySaveFailureRollsBackResource(t *testing.T) {
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	repo.failRetrySave = true
	app := newTestService(repo, time.Now)
	in := disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key}
	_, err := app.CreateDisablement(context.Background(), admin, in)
	if !errors.Is(err, disablement.ErrFailedPrecondition) || len(repo.disablements) != 0 || len(repo.keys) != 0 {
		t.Fatalf("save rollback error=%v resources=%d keys=%d", err, len(repo.disablements), len(repo.keys))
	}
	repo.failRetrySave = false
	if _, err := app.CreateDisablement(context.Background(), admin, in); err != nil || len(repo.disablements) != 1 || len(repo.keys) != 1 {
		t.Fatalf("retry error=%v resources=%d keys=%d", err, len(repo.disablements), len(repo.keys))
	}
}
