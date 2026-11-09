//go:build integration

package additionrequest_test

import (
	"errors"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestReviewRevisionCheckedAfterConcurrentEditCommits(t *testing.T) {
	f := newFixture(t)
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			f.reset(t)
			service := additionrequest.NewService(additionrequest.NewPostgresRepository(f.pool, time.Now), time.Now)
			original, err := service.SubmitRequest(
				f.ctx,
				owner,
				additionrequest.SubmitRequest{Proposal: validProposal(), Key: key},
			)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			if _, err := tx.Exec(
				f.ctx,
				"UPDATE location_addition_requests SET details='Edited during review',revision=revision+1 WHERE id=$1",
				original.ID,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(
				f.ctx,
				"DELETE FROM location_addition_request_categories WHERE request_id=$1",
				original.ID,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(
				f.ctx,
				"INSERT INTO location_addition_request_categories(request_id,category_id) VALUES($1,$2)",
				original.ID,
				otherCategoryID,
			); err != nil {
				t.Fatal(err)
			}
			decide := func(revision int64) error {
				if decision == "approve" {
					_, err := service.ApproveRequest(f.ctx, admin, original.ID, revision)
					return err
				}
				_, err := service.RejectRequest(f.ctx, admin, original.ID, "Not suitable", revision)
				return err
			}
			result := make(chan error, 1)
			go func() { result <- decide(original.Revision) }()
			// Prove the review is blocked on the edit's parent-row lock before committing.
			deadline := time.Now().Add(10 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))", int(tx.Conn().PgConn().PID())).
					Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("review did not wait for the concurrent edit")
			}
			if err := tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, additionrequest.ErrAborted) {
				t.Fatalf("stale review: got %v, want aborted", err)
			}
			current, err := service.GetRequest(f.ctx, admin, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != additionrequest.Pending || current.Revision != original.Revision+1 ||
				current.ReviewedAt != nil ||
				current.ReviewedBy != nil ||
				current.ReviewNote != nil ||
				current.ResultingLocationID != nil ||
				current.Proposal.Details != "Edited during review" ||
				len(current.Proposal.CategoryIDs) != 1 ||
				current.Proposal.CategoryIDs[0] != otherCategoryID {
				t.Fatalf("stale review changed the pending record: %+v", current)
			}
			var count int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM locations WHERE id<>$1", postgresLocationID).
				Scan(&count); err != nil ||
				count != 0 {
				t.Fatalf("stale review created locations: %d, %v", count, err)
			}
			if err := decide(current.Revision); err != nil {
				t.Fatal(err)
			}
			if err := decide(current.Revision); err != nil {
				t.Fatalf("decision retry: %v", err)
			}
		})
	}
}
