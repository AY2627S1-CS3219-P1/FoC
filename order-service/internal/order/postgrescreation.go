package order

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) Creation(ctx context.Context, orderID string) (CreationWorkflow, error) {
	id, err := parseID(orderID)
	if err != nil {
		return CreationWorkflow{}, err
	}
	row, err := db.New(r.pool).GetCreationWorkflow(ctx, id)
	if err != nil {
		return CreationWorkflow{}, persistenceError(err)
	}
	return fromCreation(row)
}

func (r *PostgresRepository) DueCreations(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	rows, err := db.New(r.pool).DueCreations(ctx, db.DueCreationsParams{NowAt: now, BatchSize: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

func (r *PostgresRepository) HasPendingCreations(ctx context.Context) (bool, error) {
	return db.New(r.pool).HasPendingCreations(ctx)
}

func (t *postgresTx) FindCreationKey(ctx context.Context, caller, key string, now time.Time) (*CreationReplay, error) {
	scope := IdempotencyScope{CallerID: caller, Method: CreateOrderMethod}
	stored, err := t.FindIdempotencyKey(ctx, scope, key)
	if err != nil || stored == nil {
		return nil, err
	}

	row, err := t.q.GetCreationWorkflowByID(ctx, uuid.MustParse(stored.ResourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, t.writeResult(ErrIdempotencyIntegrity)
	}
	if err != nil {
		return nil, err
	}
	w, err := fromCreation(row)
	if err != nil {
		return nil, t.writeResult(fmt.Errorf("%w: %v", ErrIdempotencyIntegrity, err))
	}
	if w.RequesterID != stored.Scope.CallerID || w.Hash != stored.RequestHash {
		return nil, t.writeResult(ErrIdempotencyIntegrity)
	}
	// Resolve the resource BEFORE considering expiry. A dangling expired mapping
	// cannot silently become a fresh operation; unresolved workflows remain pinned.
	if w.IsResolved() && !stored.ExpiresAt.After(now) {
		return nil, t.DeleteIdempotencyKey(ctx, stored.Scope, stored.Key)
	}
	return &CreationReplay{Key: *stored, Workflow: w}, nil
}

func (t *postgresTx) CountRecentCreations(ctx context.Context, caller string, since time.Time) (int64, error) {
	id, err := parseID(caller)
	if err != nil {
		return 0, err
	}
	if !t.idempotencyCallers[IdempotencyScope{CallerID: id.String(), Method: CreateOrderMethod}] {
		return 0, ErrPrecondition
	}
	return t.q.CountRecentCreations(ctx, db.CountRecentCreationsParams{RequesterID: id, SinceAt: since})
}

func (t *postgresTx) InsertCreation(ctx context.Context, w CreationWorkflow, key string) error {
	id, err := parseID(w.ID)
	if err != nil {
		return err
	}
	orderID, err := parseID(w.OrderID)
	if err != nil {
		return err
	}
	caller, err := parseID(w.RequesterID)
	if err != nil {
		return err
	}
	kid, err := parseID(key)
	if err != nil {
		return err
	}
	if !t.idempotencyCallers[IdempotencyScope{CallerID: caller.String(), Method: CreateOrderMethod}] ||
		!t.applied[orderID.String()] ||
		w.Phase != ReservePending {
		return t.writeResult(ErrPrecondition)
	}
	if err := t.writeResult(t.q.InsertCreationWorkflow(ctx, db.InsertCreationWorkflowParams{
		ID: id, OrderID: orderID, RequesterID: caller, CanonicalRequest: w.Canonical, RequestHash: w.Hash,
		ReservationAmount: w.Amount, ExpiresAt: w.ExpiresAt, LeaseUntil: w.LeaseUntil, NextAttemptAt: w.NextAttemptAt,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	})); err != nil {
		return err
	}
	return t.InsertIdempotencyKey(ctx, creationIdempotencyKey(w, kid.String()))
}

func (t *postgresTx) TryLockCreationOrder(ctx context.Context, orderID string) (Order, error) {
	id, err := parseID(orderID)
	if err != nil {
		return Order{}, err
	}
	row, err := t.q.TryLockCreationOrder(ctx, id)
	if err != nil {
		return Order{}, persistenceError(err)
	}
	o, err := fromOrder(row)
	if err == nil {
		t.locked[o.ID] = cloneOrder(o)
	}
	return o, err
}

func (t *postgresTx) LockCreation(ctx context.Context, orderID string) (CreationWorkflow, error) {
	id, err := parseID(orderID)
	if err != nil {
		return CreationWorkflow{}, err
	}
	if _, ok := t.locked[id.String()]; !ok {
		return CreationWorkflow{}, ErrPrecondition
	}
	row, err := t.q.LockCreationWorkflow(ctx, id)
	if err != nil {
		return CreationWorkflow{}, persistenceError(err)
	}
	w, err := fromCreation(row)
	if err != nil {
		return CreationWorkflow{}, err
	}
	if t.creationBaselines == nil {
		t.creationBaselines = map[string]CreationWorkflow{}
	}
	baseline := w
	baseline.Canonical = append(json.RawMessage(nil), w.Canonical...)
	if w.Result != nil {
		result := cloneOrder(*w.Result)
		baseline.Result = &result
	}
	t.creationBaselines[w.ID] = baseline
	return w, nil
}

func (t *postgresTx) SaveCreation(ctx context.Context, w CreationWorkflow, expected int64) error {
	before, ok := t.creationBaselines[w.ID]
	if !ok || before.IsResolved() || before.Generation != expected ||
		w.OrderID != before.OrderID || w.RequesterID != before.RequesterID ||
		w.Hash != before.Hash || !bytes.Equal(w.Canonical, before.Canonical) ||
		w.Amount != before.Amount || !w.ExpiresAt.Equal(before.ExpiresAt) ||
		!w.CreatedAt.Equal(before.CreatedAt) || w.Generation < expected ||
		w.Generation > expected+1 ||
		(before.Phase == AbortPending && w.Phase != AbortPending && w.Phase != CreationRejected) {
		return t.writeResult(ErrConflict)
	}
	if w.IsResolved() && !t.applied[w.OrderID] {
		return t.writeResult(ErrPrecondition)
	}
	var result []byte
	var err error
	if w.Result != nil {
		result, err = json.Marshal(w.Result)
		if err != nil {
			return t.writeResult(err)
		}
	}
	n, err := t.q.UpdateCreationWorkflow(ctx, db.UpdateCreationWorkflowParams{
		ID: uuid.MustParse(w.ID), Phase: string(w.Phase), Generation: w.Generation, ExpectedGeneration: expected,
		Attempts: w.Attempts, LeaseUntil: w.LeaseUntil, NextAttemptAt: w.NextAttemptAt, LastError: w.LastError,
		Rejection: string(w.Rejection), Result: result, UpdatedAt: w.UpdatedAt,
	})
	if err == nil && n != 1 {
		err = ErrConflict
	}
	return t.writeResult(err)
}

func fromCreation(row db.OrderCreationWorkflow) (CreationWorkflow, error) {
	if row.CanonicalVersion != 1 {
		return CreationWorkflow{}, fmt.Errorf("unknown creation payload version: %d", row.CanonicalVersion)
	}
	w := CreationWorkflow{
		ID:            row.ID.String(),
		OrderID:       row.OrderID.String(),
		RequesterID:   row.RequesterID.String(),
		Hash:          row.RequestHash,
		Canonical:     row.CanonicalRequest,
		Amount:        row.ReservationAmount,
		ExpiresAt:     row.ExpiresAt.UTC(),
		Phase:         CreationPhase(row.Phase),
		Generation:    row.Generation,
		Attempts:      row.Attempts,
		LeaseUntil:    row.LeaseUntil.UTC(),
		NextAttemptAt: row.NextAttemptAt.UTC(),
		LastError:     row.LastError,
		Rejection:     CreationRejection(row.Rejection),
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}
	if len(row.Result) != 0 {
		w.Result = new(Order)
		if err := json.Unmarshal(row.Result, w.Result); err != nil {
			return CreationWorkflow{}, err
		}
	}
	return w, nil
}

var _ CreationRepository = (*PostgresRepository)(nil)
