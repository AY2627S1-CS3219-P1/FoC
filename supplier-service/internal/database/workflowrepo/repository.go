// Package workflowrepo implements the workflow persistence boundary with sqlc
// and PostgreSQL. All lifecycle writes and approval are one transaction.
package workflowrepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/workflowdb"
	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func New(pool *pgxpool.Pool, clock func() time.Time) *Repository {
	return &Repository{pool: pool, clock: clock}
}

type transaction struct {
	q     *db.Queries
	clock func() time.Time
}

func (r *Repository) Within(ctx context.Context, f func(w.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if err = f(&transaction{q: db.New(tx), clock: r.clock}); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return w.ErrNotFound
	}
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23503", "23514":
			return w.ErrFailedPrecondition
		case "23505", "23P01":
			return w.ErrAlreadyExists
		case "40001":
			return w.ErrAborted
		}
	}
	return w.DependencyError("workflow persistence", err)
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
func idPointer(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	s := idString(v)
	return &s
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
func text(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
func textPointer(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
func wall(v *int64) pgtype.Time {
	if v == nil {
		return pgtype.Time{}
	}
	return pgtype.Time{Microseconds: *v, Valid: true}
}
func wallPointer(v pgtype.Time) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Microseconds
	return &n
}
func ids(v []pgtype.UUID) []string {
	out := make([]string, len(v))
	for i, u := range v {
		out[i] = idString(u)
	}
	return out
}
func disablement(v db.LocationDisablement) w.Disablement {
	return w.Disablement{ID: idString(v.ID), LocationID: idString(v.LocationID), StartsAt: v.StartsAt.Time.UTC(), EndsAt: timePointer(v.EndsAt), EndedAt: timePointer(v.EndedAt), CancelledAt: timePointer(v.CancelledAt), Reason: v.Reason, CreatedBy: v.CreatedBy, Revision: v.Revision, CreatedAt: v.CreatedAt.Time.UTC(), UpdatedAt: v.UpdatedAt.Time.UTC()}
}
func request(v db.LockWorkflowRequestRow) w.AdditionRequest {
	return w.AdditionRequest{ID: idString(v.ID), Proposal: w.Proposal{Name: v.Name, IsSupplier: v.IsSupplier, CategoryIDs: ids(v.CategoryIds), BuildingID: idString(v.BuildingID), Floor: textPointer(v.Floor), Latitude: v.Latitude, Longitude: v.Longitude, CoordinatesMissing: v.Coordinates == nil, OpenFrom: wallPointer(v.OpenFrom), OpenTo: wallPointer(v.OpenTo), Contact: textPointer(v.Contact), Details: v.Details}, SubmittedBy: v.SubmittedBy, Status: w.RequestStatus(v.Status), ReviewedBy: textPointer(v.ReviewedBy), ReviewedAt: timePointer(v.ReviewedAt), ReviewNote: textPointer(v.ReviewNote), ResultingLocationID: idPointer(v.ResultingLocationID), Revision: v.Revision, CreatedAt: v.CreatedAt.Time.UTC(), UpdatedAt: v.UpdatedAt.Time.UTC()}
}
func (t *transaction) Location(ctx context.Context, resourceID string) (w.Location, error) {
	v, e := t.q.LockWorkflowLocation(ctx, id(resourceID))
	if e != nil {
		return w.Location{}, mapError(e)
	}
	l := w.Location{ID: idString(v.ID), Proposal: w.Proposal{Name: v.Name, IsSupplier: v.IsSupplier, CategoryIDs: ids(v.CategoryIds), BuildingID: idString(v.BuildingID), Floor: textPointer(v.Floor), Latitude: v.Latitude, Longitude: v.Longitude, OpenFrom: wallPointer(v.OpenFrom), OpenTo: wallPointer(v.OpenTo), Contact: textPointer(v.Contact), Details: v.Details}, Building: w.Building{ID: idString(v.BuildingID), Name: v.BuildingName, Latitude: v.BuildingLatitude, Longitude: v.BuildingLongitude, RadiusM: v.RadiusM, CreatedAt: v.BuildingCreatedAt.Time.UTC(), UpdatedAt: v.BuildingUpdatedAt.Time.UTC()}, ArchivedAt: timePointer(v.ArchivedAt), Revision: v.Revision, CreatedAt: v.CreatedAt.Time.UTC(), UpdatedAt: v.UpdatedAt.Time.UTC()}
	cats, e := t.q.WorkflowLocationCategories(ctx, v.ID)
	if e != nil {
		return l, mapError(e)
	}
	for _, c := range cats {
		l.Categories = append(l.Categories, w.Category{ID: idString(c.ID), Name: c.Name, CreatedAt: c.CreatedAt.Time.UTC()})
	}
	d, e := t.q.CurrentWorkflowDisablement(ctx, db.CurrentWorkflowDisablementParams{LocationID: v.ID, NowAt: stamp(t.clock())})
	if e == nil {
		value := disablement(d)
		l.CurrentDisablement = &value
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return l, mapError(e)
	}
	return l, nil
}
func (t *transaction) Disablement(ctx context.Context, resourceID string) (w.Disablement, error) {
	v, e := t.q.LockWorkflowDisablement(ctx, id(resourceID))
	return disablement(v), mapError(e)
}
func (t *transaction) SaveDisablement(ctx context.Context, d w.Disablement, expected int64) error {
	if expected == 0 {
		return mapError(t.q.InsertWorkflowDisablement(ctx, db.InsertWorkflowDisablementParams{ID: id(d.ID), LocationID: id(d.LocationID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt), Reason: d.Reason, CreatedBy: d.CreatedBy, Revision: d.Revision, CreatedAt: stamp(d.CreatedAt), UpdatedAt: stamp(d.UpdatedAt)}))
	}
	n, e := t.q.UpdateWorkflowDisablement(ctx, db.UpdateWorkflowDisablementParams{ID: id(d.ID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt), EndedAt: optionalStamp(d.EndedAt), CancelledAt: optionalStamp(d.CancelledAt), Reason: d.Reason, Revision: d.Revision, UpdatedAt: stamp(d.UpdatedAt), ExpectedRevision: expected})
	if e != nil {
		return mapError(e)
	}
	if n != 1 {
		return w.ErrAborted
	}
	return nil
}
func (t *transaction) Overlaps(ctx context.Context, d w.Disablement) (bool, error) {
	v, e := t.q.WorkflowDisablementOverlap(ctx, db.WorkflowDisablementOverlapParams{LocationID: id(d.LocationID), ID: id(d.ID), StartsAt: stamp(d.StartsAt), EndsAt: optionalStamp(d.EndsAt)})
	return v, mapError(e)
}
func (t *transaction) ListDisablements(ctx context.Context, locationID string, state w.DisablementState, now time.Time, p w.Page) ([]w.Disablement, int64, error) {
	rows, e := t.q.ListWorkflowDisablements(ctx, db.ListWorkflowDisablementsParams{LocationID: id(locationID), State: string(state), NowAt: stamp(now), PageSize: p.Size, PageOffset: int64(p.Number-1) * int64(p.Size)})
	if e != nil {
		return nil, 0, mapError(e)
	}
	n, e := t.q.CountWorkflowDisablements(ctx, db.CountWorkflowDisablementsParams{LocationID: id(locationID), State: string(state), NowAt: stamp(now)})
	out := make([]w.Disablement, len(rows))
	for i, v := range rows {
		out[i] = disablement(v)
	}
	return out, n, mapError(e)
}
func (t *transaction) Request(ctx context.Context, resourceID string) (w.AdditionRequest, error) {
	v, e := t.q.LockWorkflowRequest(ctx, id(resourceID))
	if e != nil {
		return w.AdditionRequest{}, mapError(e)
	}
	v.CategoryIds, e = t.q.WorkflowRequestCategoryIDs(ctx, v.ID)
	return request(v), mapError(e)
}
func (t *transaction) SaveRequest(ctx context.Context, r w.AdditionRequest, expected int64) error {
	p := r.Proposal
	if expected == 0 {
		e := t.q.InsertWorkflowRequest(ctx, db.InsertWorkflowRequestParams{ID: id(r.ID), SubmittedBy: r.SubmittedBy, Name: p.Name, IsSupplier: p.IsSupplier, BuildingID: id(p.BuildingID), Floor: text(p.Floor), Latitude: p.Latitude, Longitude: p.Longitude, OpenFrom: wall(p.OpenFrom), OpenTo: wall(p.OpenTo), Contact: text(p.Contact), Details: p.Details, CreatedAt: stamp(r.CreatedAt)})
		if e != nil {
			return mapError(e)
		}
	} else {
		locationID := ""
		if r.ResultingLocationID != nil {
			locationID = *r.ResultingLocationID
		}
		n, e := t.q.UpdateWorkflowRequest(ctx, db.UpdateWorkflowRequestParams{CoordinatesMissing: p.CoordinatesMissing, ID: id(r.ID), Name: p.Name, IsSupplier: p.IsSupplier, BuildingID: id(p.BuildingID), Floor: text(p.Floor), Latitude: p.Latitude, Longitude: p.Longitude, OpenFrom: wall(p.OpenFrom), OpenTo: wall(p.OpenTo), Contact: text(p.Contact), Details: p.Details, Status: string(r.Status), ReviewedBy: text(r.ReviewedBy), ReviewedAt: optionalStamp(r.ReviewedAt), ReviewNote: text(r.ReviewNote), ResultingLocationID: id(locationID), Revision: r.Revision, UpdatedAt: stamp(r.UpdatedAt), ExpectedRevision: expected})
		if e != nil {
			return mapError(e)
		}
		if n != 1 {
			return w.ErrFailedPrecondition
		}
	}
	if e := t.q.DeleteWorkflowRequestCategories(ctx, id(r.ID)); e != nil {
		return mapError(e)
	}
	for _, c := range p.CategoryIDs {
		if e := t.q.InsertWorkflowRequestCategory(ctx, db.InsertWorkflowRequestCategoryParams{RequestID: id(r.ID), CategoryID: id(c)}); e != nil {
			return mapError(e)
		}
	}
	return nil
}
func (t *transaction) ListRequests(ctx context.Context, c w.Caller, status w.RequestStatus, p w.Page) ([]w.AdditionRequest, int64, error) {
	rows, e := t.q.ListWorkflowRequests(ctx, db.ListWorkflowRequestsParams{IsAdmin: c.IsAdmin(), CallerID: c.ID, StatusFilter: string(status), PageSize: p.Size, PageOffset: int64(p.Number-1) * int64(p.Size)})
	if e != nil {
		return nil, 0, mapError(e)
	}
	n, e := t.q.CountWorkflowRequests(ctx, db.CountWorkflowRequestsParams{IsAdmin: c.IsAdmin(), CallerID: c.ID, StatusFilter: string(status)})
	out := make([]w.AdditionRequest, len(rows))
	for i, v := range rows {
		out[i] = request(db.LockWorkflowRequestRow(v))
	}
	return out, n, mapError(e)
}
func (t *transaction) ValidateReferences(ctx context.Context, p w.Proposal) error {
	exists, e := t.q.WorkflowBuildingExists(ctx, id(p.BuildingID))
	if e != nil {
		return mapError(e)
	}
	if !exists {
		return w.ErrFailedPrecondition
	}
	for _, c := range p.CategoryIDs {
		exists, e = t.q.WorkflowCategoryExists(ctx, id(c))
		if e != nil {
			return mapError(e)
		}
		if !exists {
			return w.ErrFailedPrecondition
		}
	}
	return nil
}
func (t *transaction) CreateLocation(ctx context.Context, p w.Proposal, now time.Time) (w.Location, error) {
	resourceID := uuid.NewString()
	e := t.q.InsertWorkflowLocation(ctx, db.InsertWorkflowLocationParams{ID: id(resourceID), Name: p.Name, IsSupplier: p.IsSupplier, BuildingID: id(p.BuildingID), Floor: text(p.Floor), Latitude: p.Latitude, Longitude: p.Longitude, OpenFrom: wall(p.OpenFrom), OpenTo: wall(p.OpenTo), Contact: text(p.Contact), Details: p.Details, CreatedAt: stamp(now)})
	if e != nil {
		return w.Location{}, mapError(e)
	}
	for _, c := range p.CategoryIDs {
		if e = t.q.InsertWorkflowLocationCategory(ctx, db.InsertWorkflowLocationCategoryParams{LocationID: id(resourceID), CategoryID: id(c)}); e != nil {
			return w.Location{}, mapError(e)
		}
	}
	return t.Location(ctx, resourceID)
}
func (t *transaction) Idempotency(ctx context.Context, s w.IdempotencyScope, now time.Time) (*w.IdempotencyRecord, error) {
	// A structured scope prevents delimiter collisions; the advisory lock also
	// serializes the case where no idempotency row exists yet.
	scope, _ := json.Marshal(s)
	if e := t.q.LockWorkflowIdempotency(ctx, string(scope)); e != nil {
		return nil, mapError(e)
	}
	if e := t.q.DeleteExpiredWorkflowIdempotency(ctx, stamp(now)); e != nil {
		return nil, mapError(e)
	}
	v, e := t.q.GetWorkflowIdempotency(ctx, db.GetWorkflowIdempotencyParams{CallerID: s.Caller, Method: s.Method, Key: id(s.Key)})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, mapError(e)
	}
	return &w.IdempotencyRecord{Hash: v.RequestHash, ResourceID: idString(v.ResourceID), ExpiresAt: v.ExpiresAt.Time.UTC()}, nil
}
func (t *transaction) SaveIdempotency(ctx context.Context, s w.IdempotencyScope, v w.IdempotencyRecord) error {
	return mapError(t.q.InsertWorkflowIdempotency(ctx, db.InsertWorkflowIdempotencyParams{CallerID: s.Caller, Method: s.Method, Key: id(s.Key), RequestHash: v.Hash, ResourceID: id(v.ResourceID), ExpiresAt: stamp(v.ExpiresAt)}))
}

var _ w.Repository = (*Repository)(nil)
var _ w.Tx = (*transaction)(nil)
