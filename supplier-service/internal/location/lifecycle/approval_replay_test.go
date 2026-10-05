package lifecycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	workflows "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
)

func TestApprovalReplayWithoutResultingLocation(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.requests[key] = workflows.AdditionRequest{
		ID: key, Status: workflows.Approved, SubmittedBy: owner.ID,
		ReviewedBy: ptr(admin.ID), ReviewedAt: &now, Revision: 1,
	}
	app := workflows.New(repo, func() time.Time { return now })
	for attempt := 0; attempt < 2; attempt++ {
		_, err := app.ApproveRequest(context.Background(), admin, key)
		if !errors.Is(err, workflows.ErrFailedPrecondition) {
			t.Fatalf("replay %d: got %v, want failed precondition", attempt, err)
		}
		if len(repo.locations) != 0 || repo.requests[key].Revision != 1 {
			t.Fatal("invalid approval replay changed persisted state")
		}
	}
}
