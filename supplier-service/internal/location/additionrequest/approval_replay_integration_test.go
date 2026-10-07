//go:build integration

package additionrequest_test

import (
	"errors"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestLegacyApprovalReplayWithoutResultingLocationPostGIS(t *testing.T) {
	f := newFixture(t)
	f.reset(t)
	app := additionrequest.NewService(additionrequest.NewPostgresRepository(f.pool, time.Now), time.Now)
	request, err := app.SubmitRequest(f.ctx, owner, additionrequest.SubmitRequest{Key: key, Proposal: validProposal()})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a corrupted approved request without a resulting Location.
	// NOT VALID keeps the corrupt row available while checking later writes.
	var constraint string
	if err := f.pool.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid='location_addition_requests'::regclass AND conname='requests_review_check'`).Scan(&constraint); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, statement := range []string{
		`ALTER TABLE location_addition_requests DROP CONSTRAINT requests_review_check`,
		`UPDATE location_addition_requests SET status='approved',reviewed_by='legacy-admin',reviewed_at=now()`,
		`ALTER TABLE location_addition_requests ADD CONSTRAINT requests_review_check ` + constraint + " NOT VALID",
	} {
		if _, err := tx.Exec(f.ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		_, err := app.ApproveRequest(f.ctx, admin, request.ID)
		if !errors.Is(err, additionrequest.ErrFailedPrecondition) {
			t.Fatalf("approval replay %d: got %v, want failed precondition", attempt, err)
		}
	}
	var revision int64
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT revision FROM location_addition_requests WHERE id=$1`, request.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM locations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if revision != request.Revision || count != 1 {
		t.Fatalf("invalid replay changed state: revision=%d locations=%d", revision, count)
	}
}
