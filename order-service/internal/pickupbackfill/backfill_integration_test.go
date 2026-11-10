//go:build integration

package pickupbackfill

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lookupFunc func(context.Context, string) (app.SelectableLocation, error)

func (f lookupFunc) GetLocation(ctx context.Context, id string) (app.SelectableLocation, error) {
	return f(ctx, id)
}

func TestConditionalBackfill(t *testing.T) {
	url := os.Getenv("TEST_BACKFILL_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_BACKFILL_DATABASE_URL to a disposable database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(
		ctx,
		"DROP TABLE IF EXISTS orders; CREATE TABLE orders(id uuid PRIMARY KEY, pickup jsonb NOT NULL)",
	); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 5)
	for i := range ids {
		ids[i] = uuid.New()
	}
	snapshots := []string{
		`{"location_id":"archived","name":"Historical","floor":"2","unknown":{"keep":true}}`,
		`{"location_id":"existing","coordinates":{"latitude":0,"longitude":0}}`,
		`{"location_id":"unavailable","name":"Unresolved"}`,
		`{"location_id":"partial","coordinates":{"latitude":0,"longitude":null,"extra":"keep","unknown":null}}`,
		`{"location_id":"concurrent","name":"Before"}`,
	}
	for i, data := range snapshots {
		if _, err = pool.Exec(ctx, "INSERT INTO orders VALUES($1,$2)", ids[i], data); err != nil {
			t.Fatal(err)
		}
	}
	calls := map[string]int{}
	lookup := lookupFunc(func(ctx context.Context, id string) (app.SelectableLocation, error) {
		calls[id]++
		if id == "unavailable" {
			return app.SelectableLocation{}, errors.New("unavailable")
		}
		if id == "existing" {
			t.Fatal("existing coordinates fetched")
		}
		if id == "concurrent" {
			// This would block if the command held a row lock over the RPC.
			_, err := pool.Exec(
				ctx,
				`UPDATE orders SET pickup=jsonb_set(pickup,'{name}','"Concurrent"') WHERE id=$1`,
				ids[4],
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		return app.SelectableLocation{Archived: id == "archived",
			Snapshot: app.LocationSnapshot{Coordinates: &app.Coordinates{Latitude: 1, Longitude: 103}}}, nil
	})
	dry, err := Run(ctx, pool, lookup, false, io.Discard)
	if err != nil || dry.Candidates != 4 || dry.Resolved != 3 || dry.Unresolved != 1 || dry.Updated != 0 {
		t.Fatalf("dry %+v %v", dry, err)
	}
	var missing bool
	if err := pool.QueryRow(ctx, `SELECT pickup->'coordinates' IS NULL FROM orders WHERE id=$1`, ids[0]).
		Scan(&missing); err != nil ||
		!missing {
		t.Fatalf("dry wrote: %v %v", missing, err)
	}
	applied, err := Run(ctx, pool, lookup, true, io.Discard)
	if err != nil || applied.Updated != 3 || applied.Unresolved != 1 {
		t.Fatalf("apply %+v %v", applied, err)
	}
	var preserved bool
	err = pool.QueryRow(ctx, `SELECT pickup = '{"location_id":"archived","name":"Historical","floor":"2","unknown":{"keep":true},"coordinates":{"latitude":1,"longitude":103}}'::jsonb FROM orders WHERE id=$1`, ids[0]).
		Scan(&preserved)
	if err != nil || !preserved {
		t.Fatalf("snapshot fields lost: %v %v", preserved, err)
	}
	err = pool.QueryRow(ctx, `SELECT pickup->'coordinates' = '{"latitude":0,"longitude":103,"extra":"keep","unknown":null}'::jsonb FROM orders WHERE id=$1`, ids[3]).
		Scan(&preserved)
	if err != nil || !preserved {
		t.Fatalf("existing coordinate overwritten: %v %v", preserved, err)
	}
	err = pool.QueryRow(ctx, `SELECT pickup->>'name' = 'Concurrent' FROM orders WHERE id=$1`, ids[4]).Scan(&preserved)
	if err != nil || !preserved {
		t.Fatalf("concurrent field lost: %v %v", preserved, err)
	}
	rerun, err := Run(ctx, pool, lookup, true, io.Discard)
	if err != nil || rerun.Candidates != 1 || rerun.Updated != 0 || rerun.Unresolved != 1 {
		t.Fatalf("rerun %+v %v", rerun, err)
	}
}
