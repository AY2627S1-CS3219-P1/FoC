package order

import (
	"context"
	"errors"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type accountSuspensionPostgresTx struct{ *cancellationPostgresTx }

func (r *PostgresRepository) WithinAccountSuspension(ctx context.Context, fn func(AccountSuspensionTx) error) error {
	return r.withSuspensionGate(ctx, true, func(base *postgresTx) error {
		tx := &cancellationPostgresTx{
			assignmentPostgresTx: &assignmentPostgresTx{
				postgresTx: base,
				baselines:  map[string]AssignmentAttempt{},
				written:    map[string]AssignmentAttempt{},
			},
			cancellations: map[string]Cancellation{},
			changed:       map[string]bool{},
		}
		if err := fn(&accountSuspensionPostgresTx{tx}); err != nil {
			return err
		}
		for id := range tx.changed {
			if !base.applied[id] {
				return ErrPrecondition
			}
		}
		for _, w := range tx.written {
			if !base.applied[w.OrderID] {
				return ErrPrecondition
			}
		}
		return nil
	})
}

func (t *accountSuspensionPostgresTx) AccountDecision(ctx context.Context, id string) (*AccountSuspensionDecision, error) {
	row, err := t.q.AccountSuspension(ctx, uuid.MustParse(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, persistenceError(err)
	}
	return &AccountSuspensionDecision{
		ID:        row.DecisionID.String(),
		UserID:    row.UserID.String(),
		AdminID:   row.AdminID.String(),
		Revision:  row.SuspensionRevision,
		Suspended: row.Suspended,
		Reason:    row.Reason,
	}, nil
}

func (t *accountSuspensionPostgresTx) SaveAccountDecision(ctx context.Context, d AccountSuspensionDecision) error {
	return t.writeResult(t.q.SaveAccountSuspension(ctx, db.SaveAccountSuspensionParams{
		UserID:             uuid.MustParse(d.UserID),
		DecisionID:         uuid.MustParse(d.ID),
		SuspensionRevision: d.Revision,
		Suspended:          d.Suspended,
		AdminID:            uuid.MustParse(d.AdminID),
		Reason:             d.Reason,
	}))
}

func (t *accountSuspensionPostgresTx) AccountOrderIDs(ctx context.Context, userID string) ([]string, error) {
	uid := uuid.MustParse(userID)
	rows, err := t.q.AccountSuspensionOrders(ctx, &uid)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

// The shared gate is held until commit, so this local read cannot race an
// authoritative suspension. It covers stale preflight callers and another
// courier's offer accepted by the requester, not just the authenticated actor.
func (t *postgresTx) checkSuspensionEligibility(ctx context.Context, c Change) error {
	switch c.Action {
	case Create, Assign, AcceptCounteroffer, BeginAssignment, ProposeOffer, Pickup:
	default:
		return nil
	}
	ids := map[string]bool{}
	if c.Actor.Kind == User {
		ids[c.Actor.ID] = true
	}
	if c.Action == Pickup || c.Action == Assign || c.Action == AcceptCounteroffer {
		if c.Order.CourierID != nil {
			ids[*c.Order.CourierID] = true
		}
	}
	for _, update := range c.Offers {
		if update.Offer.Status == OfferOpen || update.Offer.Status == OfferAccepting || update.Offer.Status == OfferAccepted {
			ids[update.Offer.CourierID] = true
		}
	}
	for id := range ids {
		uid, err := parseID(id)
		if err != nil {
			return err
		}
		suspended, err := t.q.IsAccountSuspended(ctx, uid)
		if err != nil {
			return err
		}
		if suspended {
			return ErrForbidden
		}
	}
	return nil
}
