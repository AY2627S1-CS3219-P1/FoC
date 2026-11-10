// Package pickupbackfill fills legacy pickup coordinates without rewriting snapshots.
package pickupbackfill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Locations interface {
	GetLocation(context.Context, string) (app.SelectableLocation, error)
}

type Report struct {
	Candidates, Resolved, Updated, Unresolved int
}

// Run reads bounded batches and closes their rows before any RPC. The conditional
// update takes a row lock only after lookup, merging coordinates into current JSON.
func Run(ctx context.Context, pool *pgxpool.Pool, locations Locations, apply bool, out io.Writer) (Report, error) {
	var report Report
	cursor := uuid.Nil
	type candidate struct {
		id       uuid.UUID
		location string
	}
	for {
		rows, err := pool.Query(ctx, `
SELECT id, coalesce(pickup->>'location_id','') FROM orders
WHERE id > $1 AND (pickup->'coordinates'->>'latitude' IS NULL OR pickup->'coordinates'->>'longitude' IS NULL)
ORDER BY id LIMIT 200`, cursor)
		if err != nil {
			return report, err
		}
		batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (candidate, error) {
			var c candidate
			err := row.Scan(&c.id, &c.location)
			return c, err
		})
		if err != nil {
			return report, err
		}
		if len(batch) == 0 {
			return report, nil
		}
		for _, c := range batch {
			cursor = c.id
			report.Candidates++
			location, err := locations.GetLocation(ctx, c.location)
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			coordinates := location.Snapshot.Coordinates
			if err != nil || coordinates == nil || !coordinates.IsValid() {
				report.Unresolved++
				if _, err := fmt.Fprintf(out, "unresolved order=%s location=%s\n", c.id, c.location); err != nil {
					return report, err
				}
				continue
			}
			report.Resolved++
			if !apply {
				continue
			}
			data, err := json.Marshal(coordinates)
			if err != nil {
				return report, err
			}
			tag, err := pool.Exec(ctx, `
UPDATE orders SET pickup = jsonb_set(pickup, '{coordinates}',
 (CASE WHEN jsonb_typeof(pickup->'coordinates') = 'object'
  THEN pickup->'coordinates' ELSE '{}'::jsonb END) ||
 jsonb_build_object(
  'latitude', coalesce(nullif(pickup->'coordinates'->'latitude', 'null'::jsonb), $3::jsonb->'latitude'),
  'longitude', coalesce(nullif(pickup->'coordinates'->'longitude', 'null'::jsonb), $3::jsonb->'longitude')))
WHERE id = $1 AND pickup->>'location_id' = $2
 AND (pickup->'coordinates'->>'latitude' IS NULL OR pickup->'coordinates'->>'longitude' IS NULL)`, c.id, c.location, data)
			if err != nil {
				return report, err
			}
			report.Updated += int(tag.RowsAffected())
		}
	}
}
