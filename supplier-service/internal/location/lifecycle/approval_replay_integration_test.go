//go:build integration

package lifecycle_test

import (
	"errors"
	"testing"
	"time"

	workflows "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
)

func TestLegacyApprovalReplayWithoutResultingLocationPostGIS(t *testing.T) {
	f := newFixture(t)
	f.reset(t)
	app := workflows.New(workflows.NewPostgresRepository(f.pool, time.Now), time.Now)
	request, err := app.SubmitRequest(f.ctx, owner, workflows.SubmitRequest{Key: key, Proposal: validProposal()})
	if err != nil {
		t.Fatal(err)
	}

	// Recreate historical data retained when the migration installs NOT VALID checks.
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
		`ALTER TABLE location_addition_requests ADD CONSTRAINT requests_review_check ` + constraint,
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
		if !errors.Is(err, workflows.ErrFailedPrecondition) {
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
