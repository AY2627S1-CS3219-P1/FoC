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

// The assignment adapter wraps, rather than replaces, the aggregate transaction.
// Its Apply delegates to the sole shared Apply, including F0's history hook.
type assignmentPostgresTx struct {
	*postgresTx
	baselines map[string]AssignmentAttempt
	written   map[string]AssignmentAttempt
}

func (r *PostgresRepository) WithinAssignment(ctx context.Context, fn func(AssignmentTx) error) error {
	return r.within(ctx, func(base *postgresTx) error {
		tx := &assignmentPostgresTx{
			postgresTx: base,
			baselines:  map[string]AssignmentAttempt{},
			written:    map[string]AssignmentAttempt{},
		}
		if err := fn(tx); err != nil {
			return err
		}
		for _, w := range tx.written {
			// Financial outcome and its offer/audit change cannot commit separately.
			if w.OfferID != "" && (w.IsResolved() || w.Generation == 0) && !base.applied[w.OrderID] {
				return ErrPrecondition
			}
		}
		return nil
	})
}

func (r *PostgresRepository) AssignmentOrderID(ctx context.Context, attemptID string) (string, error) {
	id, err := parseID(attemptID)
	if err != nil {
		return "", err
	}
	row, err := db.New(r.pool).GetAssignmentAttempt(ctx, id)
	return row.OrderID.String(), persistenceError(err)
}

func (r *PostgresRepository) OfferOrderID(ctx context.Context, offerID string) (string, error) {
	id, err := parseID(offerID)
	if err != nil {
		return "", err
	}
	orderID, err := db.New(r.pool).AssignmentOfferOrder(ctx, id)
	return orderID.String(), persistenceError(err)
}

func (r *PostgresRepository) DueAssignments(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	rows, err := db.New(r.pool).DueAssignmentAttempts(ctx, db.DueAssignmentAttemptsParams{NowAt: now, BatchSize: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

func (r *PostgresRepository) HasPendingAssignments(ctx context.Context) (bool, error) {
	return db.New(r.pool).HasPendingAssignments(ctx)
}

func fromAssignment(row db.OrderAssignmentAttempt) AssignmentAttempt {
	w := AssignmentAttempt{
		ID:             row.ID.String(),
		OrderID:        row.OrderID.String(),
		CallerID:       row.CallerID.String(),
		CourierID:      row.CourierID.String(),
		Method:         row.Method,
		RequestHash:    row.RequestHash,
		Amount:         row.Amount,
		OriginalAmount: row.OriginalAmount,
		Phase:          AssignmentPhase(row.Phase),
		Generation:     row.Generation,
		Attempts:       row.Attempts,
		LeaseUntil:     row.LeaseUntil.UTC(),
		NextAttemptAt:  row.NextAttemptAt.UTC(),
		Rejection:      row.Rejection,
		CreatedAt:      row.CreatedAt.UTC(),
		UpdatedAt:      row.UpdatedAt.UTC(),
	}
	if row.OfferID != nil {
		w.OfferID = row.OfferID.String()
	}
	if row.IdempotencyKey != nil {
		w.Key = row.IdempotencyKey.String()
	}
	return w
}

func (t *assignmentPostgresTx) remember(row db.OrderAssignmentAttempt, err error) (AssignmentAttempt, error) {
	if err != nil {
		return AssignmentAttempt{}, persistenceError(err)
	}
	w := fromAssignment(row)
	if _, locked := t.locked[w.OrderID]; !locked || !t.related[w.OrderID] {
		return AssignmentAttempt{}, ErrPrecondition
	}
	if _, found := t.baselines[w.ID]; !found {
		t.baselines[w.ID] = w
	}
	return w, nil
}

func (t *assignmentPostgresTx) HasRejectedAcceptance(ctx context.Context, orderID, callerID string) (bool, error) {
	if _, locked := t.locked[orderID]; !locked || !t.related[orderID] {
		return false, ErrPrecondition
	}
	return t.q.HasRejectedAcceptance(ctx, db.HasRejectedAcceptanceParams{
		OrderID: uuid.MustParse(orderID), CallerID: uuid.MustParse(callerID),
	})
}

func (t *assignmentPostgresTx) FindAssignment(ctx context.Context, attemptID string) (AssignmentAttempt, error) {
	id, err := parseID(attemptID)
	if err != nil {
		return AssignmentAttempt{}, err
	}
	row, err := t.q.GetAssignmentAttempt(ctx, id)
	return t.remember(row, err)
}

func (t *assignmentPostgresTx) ActiveAssignment(ctx context.Context, orderID string) (*AssignmentAttempt, error) {
	id, err := parseID(orderID)
	if err != nil {
		return nil, err
	}
	if _, locked := t.locked[id.String()]; !locked || !t.related[id.String()] {
		return nil, ErrPrecondition
	}
	row, err := t.q.ActiveAssignmentAttempt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	w, err := t.remember(row, err)
	return &w, err
}

func (t *assignmentPostgresTx) OfferAssignment(ctx context.Context, offerID string) (*AssignmentAttempt, error) {
	id, err := parseID(offerID)
	if err != nil {
		return nil, err
	}
	row, err := t.q.LatestOfferAssignment(ctx, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	w, err := t.remember(row, err)
	return &w, err
}

func (t *assignmentPostgresTx) InsertAssignment(ctx context.Context, w AssignmentAttempt) error {
	o, locked := t.locked[w.OrderID]
	if !locked || !t.related[w.OrderID] || w.Generation != 0 || w.Attempts != 0 ||
		(w.Phase != AdjustmentPending && !(w.Phase == AssignmentRejected && w.OfferID == "")) ||
		w.CourierID == o.RequesterID || w.Amount <= 0 || w.OriginalAmount <= 0 || w.OriginalAmount != o.ReservedCredits {
		return t.writeResult(ErrPrecondition)
	}
	id, err := parseID(w.ID)
	if err != nil {
		return t.writeResult(err)
	}
	caller, err := parseID(w.CallerID)
	if err != nil {
		return t.writeResult(err)
	}
	courier, err := parseID(w.CourierID)
	if err != nil {
		return t.writeResult(err)
	}
	var offer, key *uuid.UUID
	if w.OfferID != "" {
		v, err := parseID(w.OfferID)
		if err != nil {
			return t.writeResult(err)
		}
		offer = &v
	}
	if w.Method == AcceptOrderMethod {
		v, err := parseID(w.Key)
		if err != nil || caller != courier ||
			!t.idempotencyCallers[IdempotencyScope{CallerID: w.CallerID, Method: w.Method}] {
			return t.writeResult(ErrPrecondition)
		}
		key = &v
	} else if w.Method != "/order.v1.OrderService/AcceptOffer" || !o.IsRequestedBy(w.CallerID) {
		return t.writeResult(ErrForbidden)
	}
	if err := t.writeResult(t.q.InsertAssignmentAttempt(ctx, db.InsertAssignmentAttemptParams{
		ID:             id,
		OrderID:        uuid.MustParse(w.OrderID),
		OfferID:        offer,
		CallerID:       caller,
		CourierID:      courier,
		Method:         w.Method,
		IdempotencyKey: key,
		RequestHash:    w.RequestHash,
		Amount:         w.Amount,
		OriginalAmount: w.OriginalAmount,
		Phase:          string(w.Phase),
		Generation:     w.Generation,
		Attempts:       w.Attempts,
		LeaseUntil:     w.LeaseUntil,
		NextAttemptAt:  w.NextAttemptAt,
		Rejection:      w.Rejection,
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
	})); err != nil {
		return err
	}
	t.baselines[w.ID], t.written[w.ID] = w, w
	return nil
}

func (t *assignmentPostgresTx) SaveAssignment(ctx context.Context, w AssignmentAttempt, expected int64) error {
	before, found := t.baselines[w.ID]
	immutable := w
	immutable.Phase, immutable.Generation, immutable.Attempts = before.Phase, before.Generation, before.Attempts
	immutable.LeaseUntil, immutable.NextAttemptAt = before.LeaseUntil, before.NextAttemptAt
	immutable.Rejection, immutable.UpdatedAt = before.Rejection, before.UpdatedAt
	if !found || before.IsResolved() || before.Generation != expected || !reflect.DeepEqual(immutable, before) ||
		w.Generation < expected || w.Generation > expected+1 || w.Attempts < before.Attempts || w.Attempts > before.Attempts+1 ||
		(before.Phase == RevertPending && w.Phase != RevertPending && w.Phase != AssignmentRejected) {
		return t.writeResult(ErrConflict)
	}
	n, err := t.q.UpdateAssignmentAttempt(ctx, db.UpdateAssignmentAttemptParams{
		ID: uuid.MustParse(w.ID), Phase: string(w.Phase), Generation: w.Generation, ExpectedGeneration: expected,
		Attempts: w.Attempts, LeaseUntil: w.LeaseUntil, NextAttemptAt: w.NextAttemptAt,
		Rejection: w.Rejection, UpdatedAt: w.UpdatedAt,
	})
	if err == nil && n != 1 {
		err = ErrConflict
	}
	if err := t.writeResult(err); err != nil {
		return err
	}
	t.baselines[w.ID], t.written[w.ID] = w, w
	return nil
}

func (t *assignmentPostgresTx) Apply(ctx context.Context, c Change) error {
	before, locked := t.locked[c.Order.ID]
	if !locked || !t.related[c.Order.ID] {
		return t.writeResult(ErrPrecondition)
	}
	offers, err := t.q.OrderOffers(ctx, uuid.MustParse(before.ID))
	if err != nil {
		return t.writeResult(err)
	}
	var attempt *AssignmentAttempt
	if c.Action != ProposeOffer {
		for _, update := range c.Offers {
			if c.Action == ResolveAssignment || update.ExpectedStatus == OfferAccepting ||
				update.Offer.Status == OfferAccepting {
				attempt, err = t.OfferAssignment(ctx, update.Offer.ID)
				if err != nil {
					return t.writeResult(err)
				}
				break
			}
		}
	}
	if len(c.Offers) == 0 {
		return t.writeResult(ErrInvalid)
	}
	if err := validateAssignmentOffers(before, fromOffers(offers), c, attempt); err != nil {
		return t.writeResult(err)
	}
	return t.postgresTx.Apply(ctx, c)
}

var _ AssignmentRepository = (*PostgresRepository)(nil)
