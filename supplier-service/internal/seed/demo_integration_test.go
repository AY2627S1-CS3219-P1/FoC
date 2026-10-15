//go:build integration

package seed

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDemoSeedPreservesEditsResetsAndRollsBack(t *testing.T) {
	url := startPostGIS(t)
	migrateAll(t, openSQLDB(t, url))
	pool := openPool(t, url)
	ctx := context.Background()
	directory := filepath.Dir(filepath.Dir(committedSeedPaths(t).Suppliers))
	directory = filepath.Join(directory, "demo")
	now := time.Now().UTC().Truncate(time.Second)
	run := func(reset bool, at time.Time) {
		t.Helper()
		if _, err := SeedDemo(ctx, pool, directory, reset, at); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string) {
		t.Helper()
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: got %d want %d", query, got, want)
		}
	}
	snapshot := func() []string {
		t.Helper()
		var result []string
		for _, table := range []string{"buildings", "categories", "locations", "location_categories", "location_addition_requests", "location_addition_request_categories", "location_disablements"} {
			var value string
			if err := pool.QueryRow(ctx, "SELECT md5(coalesce(string_agg(j::text,'' ORDER BY j::text),'')) FROM (SELECT to_jsonb(t) j FROM "+table+" t) s").Scan(&value); err != nil {
				t.Fatal(err)
			}
			result = append(result, value)
		}
		return result
	}
	run(false, now)
	count("SELECT count(*) FROM locations", 34)
	count("SELECT count(*) FROM locations WHERE is_supplier", 28)
	count("SELECT count(*) FROM location_addition_requests WHERE status='pending'", 24)
	count("SELECT count(*) FROM location_addition_requests WHERE status='approved'", 4)
	count("SELECT count(*) FROM location_addition_requests WHERE status='rejected'", 3)
	count("SELECT count(*) FROM location_addition_requests WHERE status='withdrawn'", 3)
	count("SELECT count(*) FROM location_disablements", 32)
	count("SELECT count(*) FROM locations WHERE archived_at IS NOT NULL", 3)
	count("SELECT count(*) FROM locations WHERE name LIKE '%TOMORO%'", 3)
	count("SELECT count(*) FROM locations l WHERE l.is_supplier <> EXISTS(SELECT 1 FROM location_categories c WHERE c.location_id=l.id)", 0)
	count("SELECT count(*) FROM location_addition_requests r WHERE r.is_supplier <> EXISTS(SELECT 1 FROM location_addition_request_categories c WHERE c.request_id=r.id)", 0)
	count("SELECT count(*) FROM locations WHERE is_supplier AND open_from IS NULL", 2)
	count("SELECT count(*) FROM locations WHERE contact IS NOT NULL", 5)
	count("SELECT count(*) FROM locations WHERE open_to<open_from", 1)
	count(`SELECT count(*) FROM location_disablements a JOIN location_disablements b ON a.location_id=b.location_id AND a.id<b.id
        WHERE a.cancelled_at IS NULL AND b.cancelled_at IS NULL AND tstzrange(a.starts_at,least(a.ends_at,a.ended_at),'[)') && tstzrange(b.starts_at,least(b.ends_at,b.ended_at),'[)')`, 0)
	before := snapshot()
	run(false, now.Add(time.Hour))
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("unchanged rerun wrote rows")
	}
	exec(`UPDATE locations SET details='manual',revision=revision+1 WHERE id='f0c00004-0000-4000-8000-000000000001';
        DELETE FROM location_categories WHERE location_id='f0c00004-0000-4000-8000-000000000001' AND category_id='f0c00003-0000-4000-8000-000000000001';
        UPDATE location_addition_requests SET details='manual',revision=revision+1 WHERE id='f0c00005-0000-4000-8000-000000000001';
        UPDATE location_disablements SET ended_at=now(),revision=revision+1 WHERE id='f0c00006-0000-4000-8000-000000000001';
        INSERT INTO locations(id,name,building_id,coordinates) SELECT 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','Unrelated',building_id,coordinates FROM locations LIMIT 1;`)
	before = snapshot()
	run(false, now.Add(time.Hour))
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("normal seed overwrote edits")
	}
	run(true, now.Add(time.Hour))
	count("SELECT count(*) FROM locations WHERE name='Unrelated'", 1)
	count("SELECT count(*) FROM location_categories WHERE location_id='f0c00004-0000-4000-8000-000000000001'", 2)
	count("SELECT count(*) FROM locations WHERE id='f0c00004-0000-4000-8000-000000000001' AND details<>'manual' AND revision>2", 1)
	count("SELECT count(*) FROM location_addition_requests WHERE id='f0c00005-0000-4000-8000-000000000001' AND details<>'manual' AND revision=3", 1)
	count("SELECT count(*) FROM location_disablements WHERE id='f0c00006-0000-4000-8000-000000000001' AND ended_at IS NULL AND revision=3", 1)
	exec(`INSERT INTO location_disablements(id,location_id,starts_at,reason,created_by) VALUES
        ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','f0c00004-0000-4000-8000-000000000006',now(),'Manual conflict','test')`)
	before = snapshot()
	if _, err := SeedDemo(ctx, pool, directory, true, now.Add(2*time.Hour)); err == nil || !strings.Contains(err.Error(), "overlap manually created history") {
		t.Fatalf("reset error: %v", err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("failed reset did not roll back")
	}
}
