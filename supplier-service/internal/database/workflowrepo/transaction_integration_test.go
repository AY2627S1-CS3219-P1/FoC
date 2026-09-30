//go:build integration

package workflowrepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/workflowrepo"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
)

func TestWorkflowCallbackErrorsPostGIS(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	repository := workflowrepo.New(f.pool, func() time.Time { return now })
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"revision conflict", workflows.ErrAborted},
		{"invalid state", workflows.ErrFailedPrecondition},
		{"duplicate resource", workflows.ErrAlreadyExists},
		{"dependency error", workflows.DependencyError("save request", context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.reset(t)
			var createdID string
			err := repository.Within(f.ctx, func(tx workflows.Tx) error {
				location, err := tx.CreateLocation(f.ctx, workflows.Proposal{Name: "Rolled back Location", BuildingID: buildingID, Latitude: 1.294, Longitude: 103.774}, now)
				if err != nil {
					return err
				}
				createdID = location.ID
				return tc.err
			})
			if createdID == "" {
				t.Fatalf("Location was not created before callback failure: %v", err)
			}
			if err != tc.err {
				t.Errorf("callback error identity changed: got=%v want=%v", err, tc.err)
			}
			var count int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM locations WHERE id=$1", createdID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("callback failure did not roll back Location: count=%d error=%v", count, err)
			}
		})
	}
}
