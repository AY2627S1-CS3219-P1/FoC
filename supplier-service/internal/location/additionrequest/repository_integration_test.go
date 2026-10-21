//go:build integration

package additionrequest_test

import (
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	"github.com/google/uuid"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestApprovedLocationUsesUUIDv7(t *testing.T) {
	f := newFixture(t)
	f.reset(t)

	service := additionrequest.NewService(additionrequest.NewPostgresRepository(f.pool, time.Now), time.Now)
	owner := additionrequest.Caller{ID: "owner", Role: "user"}
	admin := additionrequest.Caller{ID: "admin", Role: "admin"}

	request, err := service.SubmitRequest(f.ctx, owner, additionrequest.SubmitRequest{
		Proposal: additionrequest.Proposal{
			Name:       "UUIDv7 location",
			BuildingID: postgresBuildingID,
			Latitude:   1.294,
			Longitude:  103.774,
		},
		Key: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	approved, err := service.ApproveRequest(f.ctx, admin, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{request.ID, approved.Location.ID} {
		if id, err := uuid.Parse(value); err != nil || id.Version() != 7 {
			t.Fatalf("generated ID %q must be UUIDv7: %v", value, err)
		}
	}

	replayed, err := service.ApproveRequest(f.ctx, admin, request.ID)
	if err != nil || replayed.Location.ID != approved.Location.ID || replayed.Request.Revision != approved.Request.Revision {
		t.Fatalf("approval retry changed identity: approved=%+v replayed=%+v error=%v", approved, replayed, err)
	}
}

func TestWorkflowPersistencePostGIS(t *testing.T) {
	f := newFixture(t)
	t.Run("Location links are read after a row lock wait", func(t *testing.T) {
		f.reset(t)
		f.exec(t, "INSERT INTO location_categories(location_id,category_id) VALUES($1,$2)", postgresLocationID, postgresCategoryID)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		for _, statement := range []string{
			"UPDATE locations SET name='Changed Location',is_supplier=true,revision=revision+1 WHERE id=$1",
			"DELETE FROM location_categories WHERE location_id=$1",
		} {
			if _, err := tx.Exec(f.ctx, statement, postgresLocationID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(f.ctx, "INSERT INTO location_categories(location_id,category_id) VALUES($1,$2)", postgresLocationID, otherCategoryID); err != nil {
			t.Fatal(err)
		}
		result := make(chan additionrequest.Location, 1)
		errors := make(chan error, 1)
		go func() {
			var got additionrequest.Location
			err := additionrequest.NewPostgresRepository(f.pool, time.Now).Within(f.ctx, func(tx additionrequest.Tx) error {
				var err error
				got, err = tx.Location(f.ctx, postgresLocationID)
				return err
			})
			result <- got
			errors <- err
		}()
		waitForLocationLock(t, f)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		got := <-result
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if got.Proposal.Name != "Changed Location" || len(got.Proposal.CategoryIDs) != 1 || got.Proposal.CategoryIDs[0] != otherCategoryID || len(got.Categories) != 1 || got.Categories[0].ID != otherCategoryID {
			t.Fatalf("Location used inconsistent row and links: %+v", got)
		}
	})

}

func waitForLocationLock(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM locations l%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Location read did not wait behind the Category edit")
}
