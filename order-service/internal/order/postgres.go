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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Within owns the transaction lifecycle and rejects a commit after any Apply
// failure, even when the callback swallows that failure.
func (r *PostgresRepository) Within(ctx context.Context, fn func(Tx) error) error {
	return r.within(ctx, func(tx *postgresTx) error { return fn(tx) })
}

func (r *PostgresRepository) WithinCreation(ctx context.Context, fn func(CreationTx) error) error {
	return r.within(ctx, func(tx *postgresTx) error { return fn(tx) })
}

func (r *PostgresRepository) within(ctx context.Context, fn func(*postgresTx) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)

	t := &postgresTx{
		q:       db.New(tx),
		locked:  map[string]Order{},
		related: map[string]bool{},
		applied: map[string]bool{},
	}
	if err = fn(t); err != nil {
		return err
	}
	if t.failed != nil {
		return t.failed
	}

	return persistenceError(tx.Commit(ctx))
}

func rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_ = tx.Rollback(cleanup)
}

// postgresTx tracks lock baselines, fresh relationship reads and applied changes.
type postgresTx struct {
	q                  *db.Queries
	locked             map[string]Order
	related, applied   map[string]bool
	failed             error
	idempotencyCallers map[IdempotencyScope]bool
	creationBaselines  map[string]CreationWorkflow
}

// LockOrder keeps an independent baseline for validating the eventual change.
func (t *postgresTx) LockOrder(ctx context.Context, id string) (Order, error) {
	uid, err := parseID(id)
	if err != nil {
		return Order{}, err
	}

	row, err := t.q.LockOrder(ctx, uid)
	if err != nil {
		return Order{}, persistenceError(err)
	}

	o, err := fromOrder(row)
	if err == nil {
		t.locked[o.ID] = cloneOrder(o)
	}

	return o, err
}

// Relationships uses a separate statement after LockOrder so a lock wait cannot
// leave relationship reads on an older statement snapshot.
func (t *postgresTx) Relationships(ctx context.Context, id string) ([]Offer, error) {
	uid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	id = uid.String()
	if _, ok := t.locked[id]; !ok {
		return nil, fmt.Errorf("relationships require parent lock: %w", ErrPrecondition)
	}

	rows, err := t.q.OrderOffers(ctx, uid)
	if err != nil {
		return nil, persistenceError(err)
	}

	t.related[id] = true
	return fromOffers(rows), nil
}

// Apply writes the order, offers, audit and facts in the current transaction.
// Any failure prevents Within from committing the transaction.
func (t *postgresTx) Apply(ctx context.Context, c Change) (err error) {
	defer func() {
		if err != nil {
			t.failed = err
		}
	}()

	if t.failed != nil {
		return t.failed
	}

	// Require a fresh lock baseline for updates and allow one change per order.
	o := c.Order
	if t.applied[o.ID] {
		return ErrConflict
	}

	var before *Order
	if c.ExpectedRevision != 0 {
		current, ok := t.locked[o.ID]
		if !ok || !t.related[o.ID] {
			return fmt.Errorf("change requires locked aggregate and fresh relationships: %w", ErrPrecondition)
		}
		if current.Revision != c.ExpectedRevision {
			return ErrConflict
		}
		before = &current
	}

	// Validate the full change and convert it before the first write.
	if err := validateChange(before, c); err != nil {
		return err
	}
	row, err := toOrder(o)
	if err != nil {
		return err
	}

	// Persist the order with an optimistic revision check on updates.
	if c.ExpectedRevision == 0 {
		err = t.q.InsertOrder(ctx, db.InsertOrderParams(row))
	} else {
		n, e := t.q.UpdateOrder(ctx, db.UpdateOrderParams{
			ID:                    row.ID,
			PendingCancellationID: row.PendingCancellationID,
			CourierID:             row.CourierID,
			Status:                row.Status,
			Pickup:                row.Pickup,
			Delivery:              row.Delivery,
			DeliveryInstructions:  row.DeliveryInstructions,
			ItemDescription:       row.ItemDescription,
			RewardCredits:         row.RewardCredits,
			ReservedCredits:       row.ReservedCredits,
			DeliveryDurationNs:    row.DeliveryDurationNs,
			ExpiresAt:             row.ExpiresAt,
			AssignedAt:            row.AssignedAt,
			PickedUpAt:            row.PickedUpAt,
			DeliveredAt:           row.DeliveredAt,
			CompletedAt:           row.CompletedAt,
			ClosedAt:              row.ClosedAt,
			CloseReason:           row.CloseReason,
			ItemLocation:          row.ItemLocation,
			UnattendedDelivery:    row.UnattendedDelivery,
			Revision:              row.Revision,
			UpdatedAt:             row.UpdatedAt,
			ExpectedRevision:      c.ExpectedRevision,
		})
		err = e
		if err == nil && n != 1 {
			err = ErrConflict
		}
	}
	if err != nil {
		return persistenceError(err)
	}

	// Assignment history shares this transaction, including its rollback poison.
	if err = t.applyAssignmentHistory(ctx, before, c); err != nil {
		return err
	}

	// Offer writes check their expected status within the same transaction.
	for _, change := range c.Offers {
		f := change.Offer
		if f.OrderID != o.ID {
			return ErrInvalid
		}

		fid, e := parseID(f.ID)
		if e != nil {
			return e
		}
		courier, e := parseID(f.CourierID)
		if e != nil {
			return e
		}

		if change.ExpectedStatus == "" {
			err = t.q.InsertOffer(ctx, db.InsertOfferParams{
				ID:        fid,
				OrderID:   row.ID,
				CourierID: courier,
				Credits:   f.Credits,
				Status:    string(f.Status),
				CreatedAt: f.CreatedAt,
				UpdatedAt: f.UpdatedAt,
			})
		} else {
			n, e := t.q.UpdateOffer(ctx, db.UpdateOfferParams{
				ID:             fid,
				OrderID:        row.ID,
				CourierID:      courier,
				Credits:        f.Credits,
				Status:         string(f.Status),
				UpdatedAt:      f.UpdatedAt,
				ExpectedStatus: string(change.ExpectedStatus),
			})
			err = e
			if err == nil && n != 1 {
				err = ErrConflict
			}
		}
		if err != nil {
			return persistenceError(err)
		}
	}

	// Audit and outbox facts commit with the aggregate changes above.
	var actorID *uuid.UUID
	if c.Actor.ID != "" {
		v, e := parseID(c.Actor.ID)
		if e != nil {
			return e
		}
		actorID = &v
	}

	err = t.q.InsertAudit(ctx, db.InsertAuditParams{
		OrderID:         row.ID,
		Revision:        o.Revision,
		Action:          string(c.Action),
		ActorKind:       string(c.Actor.Kind),
		ActorID:         actorID,
		PreviousStatus:  string(c.Audit.PreviousStatus),
		ResultingStatus: string(o.Status),
		OccurredAt:      c.Audit.At,
		Details:         c.Audit.Details,
	})
	if err != nil {
		return persistenceError(err)
	}

	for i, f := range c.Facts {
		fid, e := parseID(f.ID)
		if e != nil {
			return e
		}

		if err = t.q.InsertFact(ctx, db.InsertFactParams{
			EventID:        fid,
			OrderID:        row.ID,
			Revision:       o.Revision,
			Ordinal:        int32(i),
			Kind:           string(f.Kind),
			PayloadVersion: f.Version,
			Payload:        f.Payload,
			CreatedAt:      c.Audit.At,
		}); err != nil {
			return persistenceError(err)
		}
	}

	t.applied[o.ID] = true
	return nil
}

// Validate mandatory records before writes. Caller-supplied facts cannot silently
// omit an outcome or diverge from the locked aggregate. Operations own additional
// prerequisites; this boundary only defends aggregate/fact consistency.
func validateChange(before *Order, c Change) error {
	if before != nil && (before.RequesterID != c.Order.RequesterID || !before.CreatedAt.Equal(c.Order.CreatedAt)) {
		return ErrInvalid
	}

	expected, err := NewChange(before, c.Order, c.Actor, c.Action, c.Audit.Details, func() string { return "" })
	if err != nil {
		return err
	}
	if expected.ExpectedRevision != c.ExpectedRevision || expected.Audit.PreviousStatus != c.Audit.PreviousStatus ||
		expected.Audit.Status != c.Audit.Status ||
		!expected.Audit.At.Equal(c.Audit.At) ||
		len(expected.Facts) != len(c.Facts) {
		return ErrInvalid
	}

	seen := map[string]bool{}
	for i, f := range c.Facts {
		if _, err := parseID(f.ID); err != nil {
			return err
		}
		if seen[f.ID] {
			return ErrInvalid
		}
		seen[f.ID] = true
		if f.Kind != expected.Facts[i].Kind || f.Version != expected.Facts[i].Version ||
			!jsonEqual(f.Payload, expected.Facts[i].Payload) {
			return ErrInvalid
		}
	}

	return nil
}

// Persistence helpers compare fact payloads and translate database failures.
func jsonEqual(a, b []byte) bool {
	var x, y any
	decode := func(data []byte, v any) error {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		return d.Decode(v)
	}

	if decode(a, &x) != nil || decode(b, &y) != nil {
		return false
	}

	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return string(aa) == string(bb)
}

func persistenceError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}

	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return fmt.Errorf("%w: %s", ErrConflict, p.ConstraintName)
	}

	return err
}

func parseID(id string) (uuid.UUID, error) {
	v, err := uuid.Parse(id)
	if err != nil || v == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}

	return v, nil
}

var _ Repository = (*PostgresRepository)(nil)
var _ Reader = (*PostgresRepository)(nil)

// writeResult poisons the shared transaction after a failed persistence write.
func (t *postgresTx) writeResult(err error) error {
	err = persistenceError(err)
	if err != nil {
		t.failed = err
	}
	return err
}
