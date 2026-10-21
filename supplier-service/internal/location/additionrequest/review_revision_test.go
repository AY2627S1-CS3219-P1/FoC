package additionrequest_test

import (
	"context"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestReviewRequiresTheCurrentPendingRevision(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			ctx := context.Background()
			repo := newTestRepository()
			service := newTestService(repo, time.Now)
			original, err := service.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
			if err != nil {
				t.Fatal(err)
			}
			decide := func(revision int64) error {
				if decision == "approve" {
					_, err := service.ApproveRequest(ctx, admin, original.ID, revision)
					return err
				}
				_, err := service.RejectRequest(ctx, admin, original.ID, "not suitable", revision)
				return err
			}
			for _, revision := range []int64{0, -1} {
				expectError(t, decide(revision), additionrequest.ErrInvalidArgument)
			}
			updated, err := service.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: original.ID, ExpectedRevision: original.Revision, Paths: []string{"details"}, Proposal: additionrequest.Proposal{Details: "Changed after the administrator reviewed it"}})
			if err != nil {
				t.Fatal(err)
			}
			expectError(t, decide(original.Revision), additionrequest.ErrAborted)
			stored, err := service.GetRequest(ctx, owner, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != additionrequest.Pending || stored.Revision != updated.Revision || stored.ReviewedAt != nil || stored.ReviewedBy != nil || stored.ResultingLocationID != nil || stored.ReviewNote != nil || len(repo.locations) != 0 {
				t.Fatalf("stale review wrote a decision or location: %+v", stored)
			}
			if err := decide(updated.Revision); err != nil {
				t.Fatal(err)
			}
			terminal := copyValue(repo.requests[original.ID])
			if err := decide(updated.Revision); err != nil {
				t.Fatalf("retry original review: %v", err)
			}
			got := repo.requests[original.ID]
			if got.Revision != terminal.Revision || !got.UpdatedAt.Equal(terminal.UpdatedAt) || got.Status != terminal.Status {
				t.Fatal("retry changed the terminal decision")
			}
			if decision == "approve" && len(repo.locations) != 1 {
				t.Fatalf("approval/retry created %d locations", len(repo.locations))
			}
			if decision == "reject" && len(repo.locations) != 0 {
				t.Fatal("rejection created a location")
			}
		})
	}
}
