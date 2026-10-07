//go:build integration

package location_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	goose "github.com/pressly/goose/v3"
)

// Fresh databases and migration replay must produce the same workflow schema.
// Exercise the database constraints and application-owned audit timestamps,
// rather than the removed upgrade path from the earlier workflow tables.
func TestWorkflowSchemaFreshAndReplayPostGIS(t *testing.T) {
	f := newFixture(t)
	for _, phase := range []string{"replayed"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "replayed" {
				if err := goose.DownToContext(f.ctx, f.db, f.migrations, 6); err != nil {
					t.Fatal(err)
				}
				var removed bool
				if err := f.pool.QueryRow(f.ctx, `SELECT to_regclass('location_disablements') IS NULL
					AND to_regclass('location_addition_requests') IS NULL
					AND to_regclass('location_addition_request_categories') IS NULL`).Scan(&removed); err != nil || !removed {
					t.Fatalf("workflow tables not removed: removed=%v error=%v", removed, err)
				}
				if err := goose.UpContext(f.ctx, f.db, f.migrations); err != nil {
					t.Fatal(err)
				}
			}
			f.reset(t)
			if version, err := goose.GetDBVersion(f.db); err != nil || version != 10 {
				t.Fatalf("schema version=%d error=%v", version, err)
			}
			var validated int
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_constraint WHERE convalidated
				AND conname IN ('disablements_revision_check', 'disablements_reason_check', 'disablements_terminal_check',
				'requests_revision_check', 'requests_status_check', 'requests_review_check', 'requests_proposal_check')`).Scan(&validated); err != nil || validated != 7 {
				t.Fatalf("validated workflow constraints=%d error=%v", validated, err)
			}
			var triggers int
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_trigger
				WHERE tgrelid='location_addition_requests'::regclass AND NOT tgisinternal`).Scan(&triggers); err != nil || triggers != 0 {
				t.Fatalf("request audit time must be application-owned: triggers=%d error=%v", triggers, err)
			}

			requestID := uuid.NewString()
			auditTime := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			f.exec(t, `INSERT INTO location_addition_requests(id,submitted_by,name,is_supplier,building_id,coordinates,floor,updated_at)
				VALUES($1,'owner','New Cafe',true,$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography,'2',$3)`, requestID, postgresBuildingID, auditTime)
			f.exec(t, `INSERT INTO location_addition_request_categories(request_id,category_id) VALUES($1,$2),($1,$3)`, requestID, postgresCategoryID, otherCategoryID)
			f.exec(t, `UPDATE location_addition_requests SET details='Pickup upstairs',revision=revision+1 WHERE id=$1`, requestID)
			var categories int
			var revision int64
			var persistedTime time.Time
			if err := f.pool.QueryRow(f.ctx, `SELECT revision,updated_at,
				(SELECT count(*) FROM location_addition_request_categories WHERE request_id=r.id)
				FROM location_addition_requests r WHERE id=$1`, requestID).Scan(&revision, &persistedTime, &categories); err != nil || revision != 2 || categories != 2 || !persistedTime.Equal(auditTime) {
				t.Fatalf("request persistence: revision=%d categories=%d audit=%v error=%v", revision, categories, persistedTime, err)
			}
			for _, update := range []string{
				`status='rejected',reviewed_by='admin',reviewed_at=now(),review_note='Duplicate'`,
				`status='withdrawn',reviewed_by=NULL,reviewed_at=NULL,review_note=NULL`,
				`status='approved',reviewed_by='admin',reviewed_at=now(),resulting_location_id='` + postgresLocationID + `'`,
			} {
				f.exec(t, `UPDATE location_addition_requests SET `+update+` WHERE id=$1`, requestID)
			}
			for _, tc := range []struct{ name, update string }{
				{"revision must be positive", `revision=0`},
				{"status must be known", `status='unknown'`},
				{"approval needs a Location", `resulting_location_id=NULL`},
				{"rejection needs a note", `status='rejected',resulting_location_id=NULL,review_note=' '`},
				{"proposal needs coordinates", `coordinates=NULL`},
				{"floor length is bounded", `floor=repeat('x',51)`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := f.pool.Exec(f.ctx, `UPDATE location_addition_requests SET `+tc.update+` WHERE id=$1`, requestID)
					assertCheckViolation(t, err)
				})
			}

			disablementID := uuid.NewString()
			f.exec(t, `INSERT INTO location_disablements(id,location_id,starts_at,ends_at,reason,created_by)
				VALUES($1,$2,'2026-09-01 10:00Z','2026-09-01 12:00Z','Maintenance','admin')`, disablementID, postgresLocationID)
			f.exec(t, `UPDATE location_disablements SET ended_at='2026-09-01 11:00Z',revision=2 WHERE id=$1`, disablementID)
			for _, tc := range []struct{ name, update string }{
				{"disablement revision must be positive", `revision=0`},
				{"disablement needs a reason", `reason=' '`},
				{"end cannot precede start", `ended_at='2026-09-01 09:00Z'`},
				{"end cannot exceed scheduled end", `ended_at='2026-09-01 13:00Z'`},
				{"cancel and end are exclusive", `cancelled_at='2026-09-01 09:00Z'`},
				{"cancel must precede start", `ended_at=NULL,cancelled_at='2026-09-01 11:00Z'`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := f.pool.Exec(f.ctx, `UPDATE location_disablements SET `+tc.update+` WHERE id=$1`, disablementID)
					assertCheckViolation(t, err)
				})
			}
			f.exec(t, `UPDATE location_disablements SET ended_at=NULL,cancelled_at='2026-09-01 09:00Z' WHERE id=$1`, disablementID)
		})
	}
}

func assertCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("got %v, want PostgreSQL check violation", err)
	}
}
