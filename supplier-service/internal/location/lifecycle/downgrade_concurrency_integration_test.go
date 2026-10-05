//go:build integration

package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestWorkflowDowngradeWaitsForWriterPostGIS(t *testing.T) {
	f := newFixture(t)
	f.reset(t)
	requestID := uuid.NewString()
	f.exec(t, "INSERT INTO location_addition_requests(id,submitted_by,name,is_supplier,building_id,coordinates) VALUES($1,'owner','Pending request',false,$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography)", requestID, postgresBuildingID)
	writer, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.WithoutCancel(f.ctx)) //nolint:errcheck
	if _, err := writer.Exec(f.ctx, "UPDATE location_addition_requests SET floor='B1' WHERE id=$1", requestID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- goose.DownContext(f.ctx, f.db, f.migrations) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%location_addition_requests%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("downgrade did not wait behind the request writer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := writer.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("downgrade discarded a concurrently committed request floor")
	}
	var floor string
	if err := f.pool.QueryRow(f.ctx, "SELECT floor FROM location_addition_requests WHERE id=$1", requestID).Scan(&floor); err != nil || floor != "B1" {
		t.Fatalf("concurrent floor was not retained: floor=%q error=%v", floor, err)
	}
	if version, err := goose.GetDBVersion(f.db); err != nil || version != 11 {
		t.Fatalf("refused concurrent downgrade changed version: version=%d error=%v", version, err)
	}
}
