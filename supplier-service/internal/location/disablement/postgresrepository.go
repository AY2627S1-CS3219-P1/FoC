// Each mutation commits its writes and persisted readback in one transaction.
package disablement

import (
	"context"
	"errors"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/lifecycledb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func NewPostgresRepository(pool *pgxpool.Pool, clock func() time.Time) *PostgresRepository {
	return &PostgresRepository{pool: pool, clock: clock}
}

type transaction struct {
	q          *db.Queries
	retryStore idempotency.Store
	clock      func() time.Time
}

func (r *PostgresRepository) Within(ctx context.Context, f func(Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if err = f(&transaction{q: db.New(tx), clock: r.clock, retryStore: mappedIdempotencyStore{idempotency.NewPostgresStore(tx)}}); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23503", "23514":
			return ErrFailedPrecondition
		case "23505", "23P01":
			return ErrAlreadyExists
		case "40001":
			return ErrAborted
		}
	}
	return DependencyError("workflow persistence", err)
}
func id(v string) pgtype.UUID {
	if v == "" {
		return pgtype.UUID{}
	}
	u, e := uuid.Parse(v)
	if e != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}
func idString(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}
func stamp(v time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: v.UTC(), Valid: true} }
func optionalStamp(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return stamp(*v)
}
func timePointer(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}
func disablement(v db.LocationDisablement) Disablement {
	return Disablement{ID: idString(v.ID), LocationID: idString(v.LocationID), StartsAt: v.StartsAt.Time.UTC(), EndsAt: timePointer(v.EndsAt), EndedAt: timePointer(v.EndedAt), CancelledAt: timePointer(v.CancelledAt), Reason: v.Reason, CreatedBy: v.CreatedBy, Revision: v.Revision, CreatedAt: v.CreatedAt.Time.UTC(), UpdatedAt: v.UpdatedAt.Time.UTC()}
}
func (t *transaction) Location(ctx context.Context, resourceID string) (Location, error) {
	v, err := t.q.LockWorkflowLocation(ctx, id(resourceID))
	return Location{ID: idString(v.ID), ArchivedAt: timePointer(v.ArchivedAt)}, mapError(err)
}
func (t *transaction) Disablement(ctx context.Context, resourceID string) (Disablement, error) {
	v, e := t.q.LockWorkflowDisablement(ctx, id(resourceID))
	return disablement(v), mapError(e)
}
func (t *transaction) SaveDisablement(ctx context.Context, d Disablement, expected int64) error {
	if expected == 0 {
		return mapError(t.q.InsertWorkflowDisablement(ctx, db.InsertWorkflowDisablementParams{ID: id(d.ID), LocationID: id(d.LocationID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt), Reason: d.Reason, CreatedBy: d.CreatedBy, Revision: d.Revision, CreatedAt: stamp(d.CreatedAt), UpdatedAt: stamp(d.UpdatedAt)}))
	}
	n, e := t.q.UpdateWorkflowDisablement(ctx, db.UpdateWorkflowDisablementParams{ID: id(d.ID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt), EndedAt: optionalStamp(d.EndedAt), CancelledAt: optionalStamp(d.CancelledAt), Reason: d.Reason, Revision: d.Revision, UpdatedAt: stamp(d.UpdatedAt), ExpectedRevision: expected})
	if e != nil {
		return mapError(e)
	}
	if n != 1 {
		return ErrAborted
	}
	return nil
}
func (t *transaction) Overlaps(ctx context.Context, d Disablement) (bool, error) {
	v, e := t.q.WorkflowDisablementOverlap(ctx, db.WorkflowDisablementOverlapParams{LocationID: id(d.LocationID), ID: id(d.ID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt)})
	return v, mapError(e)
}
func (t *transaction) ListDisablements(ctx context.Context, locationID string, state DisablementState, now time.Time, p Page) ([]Disablement, int64, error) {
	rows, e := t.q.ListWorkflowDisablements(ctx, db.ListWorkflowDisablementsParams{LocationID: id(locationID), State: string(state), NowAt: stamp(now), PageSize: p.Size, PageOffset: int64(p.Number-1) * int64(p.Size)})
	if e != nil {
		return nil, 0, mapError(e)
	}
	n, e := t.q.CountWorkflowDisablements(ctx, db.CountWorkflowDisablementsParams{LocationID: id(locationID), State: string(state), NowAt: stamp(now)})
	out := make([]Disablement, len(rows))
	for i, v := range rows {
		out[i] = disablement(v)
	}
	return out, n, mapError(e)
}
func (t *transaction) Idempotency() idempotency.Store { return t.retryStore }

// Preserve lifecycle error semantics while sharing retry storage and policy.
type mappedIdempotencyStore struct{ idempotency.Store }

func (s mappedIdempotencyStore) Lock(ctx context.Context, scope idempotency.Scope) error {
	return mapError(s.Store.Lock(ctx, scope))
}
func (s mappedIdempotencyStore) Find(ctx context.Context, scope idempotency.Scope, now time.Time) (*idempotency.Record, error) {
	record, err := s.Store.Find(ctx, scope, now)
	return record, mapError(err)
}
func (s mappedIdempotencyStore) Save(ctx context.Context, scope idempotency.Scope, record idempotency.Record) error {
	return mapError(s.Store.Save(ctx, scope, record))
}

var _ Repository = (*PostgresRepository)(nil)
var _ Tx = (*transaction)(nil)
