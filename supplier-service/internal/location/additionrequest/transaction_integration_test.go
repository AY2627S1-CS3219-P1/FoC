//go:build integration

package additionrequest_test

import (
	"context"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestWorkflowCallbackErrorsPostGIS(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	repository := additionrequest.NewPostgresRepository(f.pool, func() time.Time { return now })
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"revision conflict", additionrequest.ErrAborted},
		{"invalid state", additionrequest.ErrFailedPrecondition},
		{"duplicate resource", additionrequest.ErrAlreadyExists},
		{"dependency error", additionrequest.DependencyError("save request", context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.reset(t)
			var createdID string
			err := repository.Within(f.ctx, func(tx additionrequest.Tx) error {
				location, err := tx.CreateLocation(f.ctx, additionrequest.Proposal{Name: "Rolled back Location", BuildingID: postgresBuildingID, Latitude: 1.294, Longitude: 103.774}, now)
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
