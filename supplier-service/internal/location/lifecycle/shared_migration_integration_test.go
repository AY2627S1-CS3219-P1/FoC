//go:build integration

package lifecycle_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	repo "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestWorkflowMigrationVersions(t *testing.T) {
	migrations, err := goose.CollectMigrations(filepath.Join("..", "..", "..", "database", "schema"), 0, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	if migrations[len(migrations)-1].Version != 11 {
		t.Fatal("workflow migration must follow the legacy-user removal migration")
	}
}

func TestWorkflowDowngradePreservesSharedRetriesPostGIS(t *testing.T) {
	f := newFixture(t)
	f.reset(t)
	scope := idempotency.Scope{Caller: "shared owner", Method: "shared method", Key: uuid.NewString()}
	record := idempotency.Record{Hash: strings.Repeat("a", 64), ResourceID: uuid.NewString(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	r := repo.NewPostgresRepository(f.pool, time.Now)
	if err := r.Within(f.ctx, func(tx w.Tx) error { return tx.Idempotency().Save(f.ctx, scope, record) }); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownContext(f.ctx, f.db, f.migrations); err != nil {
		t.Fatal(err)
	}
	if version, err := goose.GetDBVersion(f.db); err != nil || version != 10 {
		t.Fatalf("downgrade version: %d, %v", version, err)
	}
	var hash string
	if err := f.pool.QueryRow(f.ctx, "SELECT request_hash FROM supplier_idempotency WHERE caller_id=$1 AND method=$2 AND key=$3", scope.Caller, scope.Method, scope.Key).Scan(&hash); err != nil || hash != record.Hash {
		t.Fatalf("shared retry lost: %q, %v", hash, err)
	}
	if err := goose.UpContext(f.ctx, f.db, f.migrations); err != nil {
		t.Fatal(err)
	}
	if err := r.Within(f.ctx, func(tx w.Tx) error {
		if err := tx.Idempotency().Lock(f.ctx, scope); err != nil {
			return err
		}
		got, err := tx.Idempotency().Find(f.ctx, scope, time.Now())
		if err != nil {
			return err
		}
		if got == nil || got.Hash != record.Hash || got.ResourceID != record.ResourceID {
			t.Errorf("shared retry not retained after re-upgrade: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
