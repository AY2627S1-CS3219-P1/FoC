package seed

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/seeddb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedDemo imports opt-in CSV fixtures through the existing importer, then adds
// simulated workflow history through sqlc. It never applies schema migrations.
// Fixed IDs retain compatibility with previously loaded local demo records.
func SeedDemo(ctx context.Context, pool *pgxpool.Pool, directory string, reset bool, now time.Time) (Report, error) {
	data, err := load(Paths{Suppliers: filepath.Join(directory, "suppliers.csv"), Buildings: filepath.Join(directory, "buildings.csv"), OrdinaryLocations: filepath.Join(directory, "locations.csv"), AllowUnspecifiedHours: true})
	if err != nil {
		return Report{}, err
	}
	ids, err := demoSourceIDs(data)
	if err != nil {
		return Report{}, err
	}
	for i := range data.categories {
		data.categories[i].name = "[Demo] " + data.categories[i].name
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := seeddb.New(tx)
	if err := q.LockDemoSeed(ctx); err != nil {
		return Report{}, err
	}
	existing := make(map[int]bool, 34)
	// Lock existing parents before inspecting or changing relationships. Keep a
	// stable order shared by concurrent demo runs.
	for n := 1; n <= 34; n++ {
		_, err := q.LockDemoLocation(ctx, demoID(4, n))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Report{}, err
		}
		existing[n] = err == nil
	}
	report, err := importDatasetWithOptions(ctx, q, data, importOptions{idFor: func(key string) pgtype.UUID { return ids[key] }, preserveExisting: !reset, requireIDs: true})
	if err != nil {
		return Report{}, err
	}
	for n := 1; n <= 34; n++ {
		if existing[n] && !reset {
			continue
		}
		state := seeddb.SetDemoLocationStateParams{ID: demoID(4, n), ResetDemo: reset}
		if n == 4 || n == 5 || n == 33 {
			state.ArchivedAt = demoStamp(now.Add(-7 * 24 * time.Hour))
		}
		if err := q.SetDemoLocationState(ctx, state); err != nil {
			return Report{}, err
		}
	}
	if err := seedDemoRequests(ctx, q, reset, now); err != nil {
		return Report{}, fmt.Errorf("seed demo requests: %w", err)
	}
	if err := seedDemoClosures(ctx, q, reset, now); err != nil {
		return Report{}, fmt.Errorf("seed demo closures: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return report, nil
}

func demoID(kind, n int) pgtype.UUID {
	id := uuid.MustParse(fmt.Sprintf("f0c0000%d-0000-4000-8000-%012d", kind, n))
	return pgtype.UUID{Bytes: id, Valid: true}
}
func demoIdentity(n int) string                { return uuid.UUID(demoID(1, n).Bytes).String() }
func demoStamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }

func demoSourceIDs(data dataset) (map[string]pgtype.UUID, error) {
	if len(data.buildings) != 18 || len(data.locations) != 34 || len(data.categories) != 4 {
		return nil, fmt.Errorf("demo CSVs must contain 18 buildings, 34 locations and four categories")
	}
	ids := make(map[string]pgtype.UUID)
	used := make(map[pgtype.UUID]bool)
	add := func(key string, kind, max int) error {
		parts := strings.SplitN(key, ":demo-", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid demo source key %q", key)
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > max || used[demoID(kind, n)] {
			return fmt.Errorf("invalid or duplicate demo ID for %q", key)
		}
		ids[key] = demoID(kind, n)
		used[ids[key]] = true
		return nil
	}
	for _, b := range data.buildings {
		if err := add(b.sourceKey, 2, 18); err != nil {
			return nil, err
		}
	}
	for _, l := range data.locations {
		if err := add(l.sourceKey, 4, 34); err != nil {
			return nil, err
		}
	}
	categories := map[string]int{"category:food": 1, "category:drinks": 2, "category:dessert": 3, "category:coffee": 4}
	for _, c := range data.categories {
		n, ok := categories[c.sourceKey]
		if !ok {
			return nil, fmt.Errorf("unexpected demo category %q", c.sourceKey)
		}
		ids[c.sourceKey] = demoID(3, n)
	}
	return ids, nil
}

func seedDemoRequests(ctx context.Context, q *seeddb.Queries, reset bool, now time.Time) error {
	for n := 1; n <= 34; n++ {
		owner := 5
		if n%2 == 1 {
			owner = 3
		}
		p := seeddb.UpsertDemoRequestParams{ID: demoID(5, n), SubmittedBy: demoIdentity(owner), Status: "pending", Revision: 1,
			ProposalLocationID: demoID(4, n), ScenarioDetails: fmt.Sprintf("\nSimulated proposal %d.", n),
			CreatedAt: demoStamp(now.Add(-48*time.Hour - time.Duration(n)*time.Minute)), UpdatedAt: demoStamp(now.Add(-24 * time.Hour)), ResetDemo: reset}
		switch {
		case n <= 24:
		case n <= 28:
			p.Status = "approved"
			p.ResultingLocationID = demoID(4, n+6)
			p.ProposalLocationID = p.ResultingLocationID
		case n <= 31:
			p.Status = "rejected"
			p.ReviewNote = pgtype.Text{String: "[Demo] Duplicate or unsuitable proposal; try submitting a corrected location.", Valid: true}
		default:
			p.Status = "withdrawn"
		}
		if n == 4 {
			p.ScenarioDetails += " Open in two admin sessions and decide in one before confirming in the other to test a stale revision."
		}
		if p.Status != "pending" {
			p.Revision = 2
		}
		if p.Status == "approved" || p.Status == "rejected" {
			p.ReviewedBy = pgtype.Text{String: demoIdentity(2), Valid: true}
			p.ReviewedAt = demoStamp(now.Add(-24 * time.Hour))
		}
		_, err := q.UpsertDemoRequest(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) && !reset {
			continue
		}
		if err != nil {
			return err
		}
		if err := q.DeleteDemoRequestCategories(ctx, p.ID); err != nil {
			return err
		}
		if err := q.CopyDemoRequestCategories(ctx, seeddb.CopyDemoRequestCategoriesParams{RequestID: p.ID, LocationID: p.ProposalLocationID}); err != nil {
			return err
		}
	}
	return nil
}

func demoClosures(now time.Time, reset bool) []seeddb.UpsertDemoDisablementParams {
	hour := time.Hour
	day := 24 * hour
	var rows []seeddb.UpsertDemoDisablementParams
	add := func(n, location int, start time.Time, end, ended, cancelled pgtype.Timestamptz, reason string) {
		created := now.Add(-3 * day)
		if start.Before(created) {
			created = start
		}
		rows = append(rows, seeddb.UpsertDemoDisablementParams{ID: demoID(6, n), LocationID: demoID(4, location), StartsAt: demoStamp(start), EndsAt: end, EndedAt: ended, CancelledAt: cancelled, Reason: "[Demo] " + reason, CreatedBy: demoIdentity(2), CreatedAt: demoStamp(created), UpdatedAt: demoStamp(now), ResetDemo: reset})
	}
	empty := pgtype.Timestamptz{}
	for n := 6; n <= 30; n++ {
		start := now.Add(-time.Duration(n+10) * day)
		add(n, 8, start, demoStamp(start.Add(2*hour)), empty, empty, fmt.Sprintf("Completed closure %02d", n))
	}
	add(1, 6, now.Add(-2*day), empty, empty, empty, "Active indefinite repairs")
	add(2, 7, now.Add(2*day), demoStamp(now.Add(3*day)), empty, empty, "Simulated scheduled maintenance")
	add(3, 8, now.Add(-12*hour), demoStamp(now.Add(12*hour)), empty, empty, "Active finite closure")
	add(4, 8, now.Add(3*day), demoStamp(now.Add(4*day)), empty, demoStamp(now.Add(-day)), "Cancelled future maintenance")
	add(5, 8, now.Add(-5*day), demoStamp(now.Add(-3*day)), demoStamp(now.Add(-4*day)), empty, "Closure ended early")
	add(31, 8, now.Add(7*day), empty, empty, empty, "Scheduled indefinite closure")
	add(32, 34, now.Add(-4*hour), demoStamp(now.Add(day)), empty, empty, "Approved location temporarily closed")
	return rows
}

func seedDemoClosures(ctx context.Context, q *seeddb.Queries, reset bool, now time.Time) error {
	rows := demoClosures(now, reset)
	// Remove the intervals being replaced from the overlap check. All other
	// history, including manually added records, remains authoritative.
	replacing := []pgtype.UUID{}
	if reset {
		for _, r := range rows {
			replacing = append(replacing, r.ID)
		}
	}
	for _, r := range rows {
		exists, err := q.DemoDisablementExists(ctx, r.ID)
		if err != nil {
			return err
		}
		if exists && !reset {
			continue
		}
		if !r.CancelledAt.Valid {
			end := r.EndsAt
			if r.EndedAt.Valid && (!end.Valid || r.EndedAt.Time.Before(end.Time)) {
				end = r.EndedAt
			}
			overlap, err := q.DemoDisablementOverlap(ctx, seeddb.DemoDisablementOverlapParams{LocationID: r.LocationID, ReplacingIds: replacing, StartsAt: r.StartsAt, EffectiveEnd: end})
			if err != nil {
				return err
			}
			if overlap {
				return fmt.Errorf("demo closures overlap manually created history; resolve conflicting intervals before resetting")
			}
		}
	}
	for _, r := range rows {
		if err := q.UpsertDemoDisablement(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
