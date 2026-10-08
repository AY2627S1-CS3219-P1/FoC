package additionrequest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestApprovalReplayWithoutResultingLocation(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.requests[key] = additionrequest.AdditionRequest{
		ID: key, Status: additionrequest.Approved, SubmittedBy: owner.ID,
		ReviewedBy: ptr(admin.ID), ReviewedAt: &now, Revision: 1,
	}
	app := newTestService(repo, func() time.Time { return now })
	for attempt := 0; attempt < 2; attempt++ {
		_, err := app.ApproveRequest(context.Background(), admin, key)
		if !errors.Is(err, additionrequest.ErrFailedPrecondition) {
			t.Fatalf("replay %d: got %v, want failed precondition", attempt, err)
		}
		if len(repo.locations) != 0 || repo.requests[key].Revision != 1 {
			t.Fatal("invalid approval replay changed persisted state")
		}
	}
}
