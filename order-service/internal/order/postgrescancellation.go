package order

import (
	"context"
	"errors"
	"reflect"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type cancellationPostgresTx struct {
	*assignmentPostgresTx
	cancellations map[string]Cancellation
	changed       map[string]bool
}

func (r *PostgresRepository) WithinCancellation(ctx context.Context, fn func(CancellationTx) error) error {
	return r.within(ctx, func(base *postgresTx) error {
		assignments := &assignmentPostgresTx{
			postgresTx: base,
			baselines:  map[string]AssignmentAttempt{},
			written:    map[string]AssignmentAttempt{},
		}
		tx := &cancellationPostgresTx{
			assignmentPostgresTx: assignments,
			cancellations:        map[string]Cancellation{},
			changed:              map[string]bool{},
		}
		if err := fn(tx); err != nil {
			return err
		}
		for id := range tx.changed {
			if !base.applied[id] {
				return ErrPrecondition
			}
		}
		for _, w := range assignments.written {
			if !base.applied[w.OrderID] {
				return ErrPrecondition
			}
		}
		return nil
	})
}

// Cancellation uses the shared aggregate Apply and its sole history writer.
func (t *cancellationPostgresTx) Apply(ctx context.Context, c Change) error {
	return t.postgresTx.Apply(ctx, c)
}

func (r *PostgresRepository) CancellationOrderID(ctx context.Context, operationID string) (string, error) {
	id, err := parseID(operationID)
	if err != nil {
		return "", err
	}
	row, err := db.New(r.pool).GetCancellation(ctx, id)
	return row.OrderID.String(), persistenceError(err)
}

func (r *PostgresRepository) HasPendingCancellations(ctx context.Context) (bool, error) {
	return db.New(r.pool).HasPendingCancellations(ctx)
}

func (r *PostgresRepository) DueCancellations(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	rows, err := db.New(r.pool).DueCancellations(ctx, db.DueCancellationsParams{NowAt: now, BatchSize: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

func fromCancellation(row db.OrderCancellation) Cancellation {
	w := Cancellation{ID: row.ID.String(), OrderID: row.OrderID.String(), CallerID: row.CallerID.String(), Reason: row.Reason,
		Status: CancellationStatus(row.Status), AssignedAt: utcTime(row.AssignedAt), OriginalAmount: row.OriginalAmount,
		Generation: row.Generation, Attempts: row.Attempts, LeaseUntil: row.LeaseUntil.UTC(), NextAttemptAt: row.NextAttemptAt.UTC(), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
	if row.AssignmentID != nil {
		w.AssignmentID = row.AssignmentID.String()
	}
	if row.CourierID != nil {
		w.CourierID = row.CourierID.String()
	}
	return w
}

func (t *cancellationPostgresTx) FindCancellation(ctx context.Context, operationID string) (Cancellation, error) {
	id, err := parseID(operationID)
	if err != nil {
		return Cancellation{}, err
	}
	row, err := t.q.GetCancellation(ctx, id)
	if err != nil {
		return Cancellation{}, persistenceError(err)
	}
	w := fromCancellation(row)
	if _, ok := t.locked[w.OrderID]; !ok || !t.related[w.OrderID] {
		return Cancellation{}, ErrPrecondition
	}
	t.cancellations[w.ID] = w
	return w, nil
}

func (t *cancellationPostgresTx) CurrentSuccessfulAssignment(ctx context.Context, orderID string) (*AssignmentAttempt, error) {
	id, err := parseID(orderID)
	if err != nil {
		return nil, err
	}
	if _, ok := t.locked[orderID]; !ok || !t.related[orderID] {
		return nil, ErrPrecondition
	}
	row, err := t.q.CurrentSuccessfulAssignment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	w, err := t.remember(row, err)
	return &w, err
}

func optionalCancellationID(id string) *uuid.UUID {
	if id == "" {
		return nil
	}
	v := uuid.MustParse(id)
	return &v
}

func (t *cancellationPostgresTx) InsertCancellation(ctx context.Context, w Cancellation) error {
	o, ok := t.locked[w.OrderID]
	if !ok || !t.related[w.OrderID] || !o.HasParticipant(w.CallerID) || w.Generation != 0 || w.Attempts != 0 ||
		(w.Status != CancellationPending && w.Status != CancellationSucceeded) {
		return t.writeResult(ErrPrecondition)
	}
	// Values originate from validated domain intent and the locked assignment.
	err := t.q.InsertCancellation(ctx, db.InsertCancellationParams{
		ID: uuid.MustParse(w.ID), OrderID: uuid.MustParse(w.OrderID), CallerID: uuid.MustParse(w.CallerID), Reason: w.Reason, Status: string(w.Status),
		AssignmentID: optionalCancellationID(w.AssignmentID), CourierID: optionalCancellationID(w.CourierID), AssignedAt: w.AssignedAt, OriginalAmount: w.OriginalAmount,
		Generation: w.Generation, Attempts: w.Attempts, LeaseUntil: w.LeaseUntil, NextAttemptAt: w.NextAttemptAt, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	})
	if err := t.writeResult(err); err != nil {
		return err
	}
	t.cancellations[w.ID], t.changed[w.OrderID] = w, true
	return nil
}

func (t *cancellationPostgresTx) SaveCancellation(ctx context.Context, w Cancellation, expected int64) error {
	before, ok := t.cancellations[w.ID]
	immutable := w
	immutable.Status, immutable.Generation, immutable.Attempts = before.Status, before.Generation, before.Attempts
	immutable.LeaseUntil, immutable.NextAttemptAt, immutable.UpdatedAt = before.LeaseUntil, before.NextAttemptAt, before.UpdatedAt
	if !ok || before.Status != CancellationPending || before.Generation != expected || !reflect.DeepEqual(immutable, before) ||
		w.Generation < expected || w.Generation > expected+1 || w.Attempts < before.Attempts || w.Attempts > before.Attempts+1 {
		return t.writeResult(ErrConflict)
	}
	n, err := t.q.UpdateCancellation(ctx, db.UpdateCancellationParams{ID: uuid.MustParse(w.ID), Status: string(w.Status), Generation: w.Generation,
		ExpectedGeneration: expected, Attempts: w.Attempts, LeaseUntil: w.LeaseUntil, NextAttemptAt: w.NextAttemptAt, UpdatedAt: w.UpdatedAt})
	if err == nil && n != 1 {
		err = ErrConflict
	}
	if err := t.writeResult(err); err != nil {
		return err
	}
	t.cancellations[w.ID] = w
	if w.Status != CancellationPending {
		t.changed[w.OrderID] = true
	}
	return nil
}

var _ CancellationRepository = (*PostgresRepository)(nil)
