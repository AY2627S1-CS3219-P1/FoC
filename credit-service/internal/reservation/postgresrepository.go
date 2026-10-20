package reservation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const insufficientCredits = "INSUFFICIENT_CREDITS"

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Within runs f in a READ COMMITTED transaction. Rows are locked in the order
// order → reservation → assignment → accounts (by user ID), and relationships
// are read in their own statements after the parent lock is held.
func (r *PostgresRepository) Within(ctx context.Context, f func(Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mapError("begin reservation transaction", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	if err := f(&transaction{q: creditdb.New(tx)}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return mapError("reservation transaction", err)
		}
		return err
	}

	return mapError("commit reservation transaction", tx.Commit(ctx))
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
		return fmt.Errorf("%s: %w: %w", operation, ErrAborted, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type transaction struct{ q *creditdb.Queries }

// Orders and operations

func (t *transaction) LockOrder(ctx context.Context, orderID string) error {
	return wrap("lock order", t.q.LockOrder(ctx, uuid.MustParse(orderID)))
}

func (t *transaction) Operation(ctx context.Context, key OperationKey) (*Operation, error) {
	row, err := t.q.GetOperation(ctx, creditdb.GetOperationParams{
		Operation:    string(key.Kind),
		OrderID:      uuid.MustParse(key.OrderID),
		AssignmentID: optionalID(key.AssignmentID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("get operation", err)
	}

	op := Operation{RequestHash: row.RequestHash, CreatedAt: row.CreatedAt.UTC()}
	if row.Revision != nil {
		op.Revision = *row.Revision
	}
	op.Insufficient = row.RejectionReason != nil && *row.RejectionReason == insufficientCredits
	return &op, nil
}

func (t *transaction) SaveOperation(ctx context.Context, key OperationKey, op Operation) error {
	params := creditdb.InsertOperationParams{
		Operation:    string(key.Kind),
		OrderID:      uuid.MustParse(key.OrderID),
		AssignmentID: optionalID(key.AssignmentID),
		RequestHash:  op.RequestHash,
		NowAt:        op.CreatedAt,
	}
	if op.Insufficient {
		params.RejectionReason = ptr(insufficientCredits)
	} else {
		params.Revision = &op.Revision
	}

	return wrap("save operation", t.q.InsertOperation(ctx, params))
}

// Reservations

func (t *transaction) Reservation(ctx context.Context, orderID string) (*Reservation, error) {
	row, err := t.q.LockReservation(ctx, uuid.MustParse(orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("lock reservation", err)
	}

	r := fromRow(creditdb.ReservationRevision(row))
	return &r, nil
}

func (t *transaction) Revision(ctx context.Context, orderID string, revision int64) (Reservation, error) {
	row, err := t.q.GetReservationRevision(ctx, creditdb.GetReservationRevisionParams{
		OrderID:  uuid.MustParse(orderID),
		Revision: revision,
	})
	if err != nil {
		return Reservation{}, wrap("get reservation revision", err)
	}
	return fromRow(row), nil
}

func (t *transaction) InsertReservation(ctx context.Context, r Reservation) (Reservation, error) {
	c := toColumns(r)
	row, err := t.q.InsertReservation(ctx, creditdb.InsertReservationParams{
		OrderID:             uuid.MustParse(r.OrderID),
		RequesterID:         optionalID(r.RequesterID),
		Status:              string(r.Status),
		OriginalAmount:      r.OriginalAmount,
		ReservedAmount:      r.ReservedAmount,
		AssignmentID:        c.assignmentID,
		ReleaseReason:       c.releaseReason,
		SettlementOutcome:   c.outcome,
		PaidToCourier:       c.paid,
		ReturnedToRequester: c.returned,
		PickedUpAt:          c.pickedUpAt,
		DeliveredAt:         c.deliveredAt,
		DeliveryDuration:    c.duration,
		SettledAt:           c.settledAt,
		Revision:            r.Revision,
		NowAt:               r.UpdatedAt,
	})
	if err != nil {
		return Reservation{}, wrap("insert reservation", err)
	}
	return fromRow(creditdb.ReservationRevision(row)), nil
}

func (t *transaction) UpdateReservation(ctx context.Context, r Reservation, expectedRevision int64) (Reservation, error) {
	c := toColumns(r)
	row, err := t.q.UpdateReservation(ctx, creditdb.UpdateReservationParams{
		OrderID:             uuid.MustParse(r.OrderID),
		Status:              string(r.Status),
		ReservedAmount:      r.ReservedAmount,
		AssignmentID:        c.assignmentID,
		ReleaseReason:       c.releaseReason,
		SettlementOutcome:   c.outcome,
		PaidToCourier:       c.paid,
		ReturnedToRequester: c.returned,
		PickedUpAt:          c.pickedUpAt,
		DeliveredAt:         c.deliveredAt,
		DeliveryDuration:    c.duration,
		SettledAt:           c.settledAt,
		ExpectedRevision:    expectedRevision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, fmt.Errorf("update reservation %s at revision %d: %w", r.OrderID, expectedRevision, ErrAborted)
	}
	if err != nil {
		return Reservation{}, wrap("update reservation", err)
	}
	return fromRow(creditdb.ReservationRevision(row)), nil
}

// Assignments

func (t *transaction) Assignment(ctx context.Context, orderID, assignmentID string) (*Assignment, error) {
	row, err := t.q.GetAssignment(ctx, creditdb.GetAssignmentParams{
		OrderID:      uuid.MustParse(orderID),
		AssignmentID: uuid.MustParse(assignmentID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("get assignment", err)
	}

	a := Assignment{
		OrderID:      row.OrderID.String(),
		AssignmentID: row.AssignmentID.String(),
		CourierID:    idString(row.CourierID),
		Status:       AdjustmentStatus(row.Status),
		CreatedAt:    row.CreatedAt.UTC(),
		RevertedAt:   utc(row.RevertedAt),
	}
	if row.OfferAmount != nil {
		a.OfferAmount = *row.OfferAmount
	}
	return &a, nil
}

func (t *transaction) InsertAssignment(ctx context.Context, a Assignment) error {
	params := creditdb.InsertAssignmentParams{
		OrderID:      uuid.MustParse(a.OrderID),
		AssignmentID: uuid.MustParse(a.AssignmentID),
		CourierID:    optionalID(a.CourierID),
		Status:       string(a.Status),
		NowAt:        a.CreatedAt,
		RevertedAt:   a.RevertedAt,
	}
	if a.CourierID != "" {
		params.OfferAmount = &a.OfferAmount
	}

	return wrap("insert assignment", t.q.InsertAssignment(ctx, params))
}

func (t *transaction) RevertAssignment(ctx context.Context, orderID, assignmentID string, at time.Time) error {
	n, err := t.q.RevertAssignment(ctx, creditdb.RevertAssignmentParams{
		OrderID:      uuid.MustParse(orderID),
		AssignmentID: uuid.MustParse(assignmentID),
		NowAt:        at,
	})
	if err != nil {
		return wrap("revert assignment", err)
	}
	if n != 1 {
		return fmt.Errorf("revert assignment %s: %w", assignmentID, ErrAborted)
	}
	return nil
}

// Accounts

func (t *transaction) LockAccounts(ctx context.Context, at time.Time, userIDs ...string) (map[string]account.Account, error) {
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)

	out := make(map[string]account.Account, len(ids))
	for _, id := range ids {
		a, err := account.Open(ctx, t.q, at, uuid.MustParse(id))
		if err != nil {
			return nil, err
		}
		out[id] = account.FromRow(a)
	}
	return out, nil
}

func (t *transaction) Post(ctx context.Context, at time.Time, entries ...Entry) error {
	postings := make([]account.Posting, len(entries))
	for i, e := range entries {
		postings[i] = account.Posting{
			UserID:        uuid.MustParse(e.UserID),
			Kind:          e.Kind,
			BalanceDelta:  e.BalanceDelta,
			ReservedDelta: e.ReservedDelta,
			OrderID:       optionalID(e.OrderID),
			AssignmentID:  optionalID(e.AssignmentID),
		}
	}

	return account.Post(ctx, t.q, at, postings...)
}

// Row conversion

type columns struct {
	assignmentID                       *uuid.UUID
	releaseReason, outcome             *string
	paid, returned                     *int64
	pickedUpAt, deliveredAt, settledAt *time.Time
	duration                           pgtype.Interval
}

func toColumns(r Reservation) columns {
	c := columns{assignmentID: optionalID(r.AssignmentID)}
	s := r.Settlement
	if s == nil {
		return c
	}

	c.outcome = ptr(string(s.Outcome))
	c.paid, c.returned, c.settledAt = &s.PaidToCourier, &s.ReturnedToRequester, &s.SettledAt
	if s.ReleaseReason != "" {
		c.releaseReason = ptr(string(s.ReleaseReason))
	}
	if d := s.Completion; d != nil {
		c.pickedUpAt, c.deliveredAt = &d.PickedUpAt, &d.DeliveredAt
		c.duration = pgtype.Interval{Microseconds: d.DeliveryDuration.Microseconds(), Valid: true}
	}
	return c
}

// fromRow reads a reservation or one of its revisions; both share columns.
func fromRow(row creditdb.ReservationRevision) Reservation {
	r := Reservation{
		OrderID:        row.OrderID.String(),
		RequesterID:    idString(row.RequesterID),
		Status:         Status(row.Status),
		OriginalAmount: row.OriginalAmount,
		ReservedAmount: row.ReservedAmount,
		AssignmentID:   idString(row.AssignmentID),
		Revision:       row.Revision,
		CreatedAt:      row.CreatedAt.UTC(),
		UpdatedAt:      row.UpdatedAt.UTC(),
	}
	if row.SettlementOutcome == nil {
		return r
	}

	s := &Settlement{Outcome: SettlementOutcome(*row.SettlementOutcome)}
	if row.SettledAt != nil {
		s.SettledAt = row.SettledAt.UTC()
	}
	if row.PaidToCourier != nil {
		s.PaidToCourier = *row.PaidToCourier
	}
	if row.ReturnedToRequester != nil {
		s.ReturnedToRequester = *row.ReturnedToRequester
	}
	if row.ReleaseReason != nil {
		s.ReleaseReason = ReleaseReason(*row.ReleaseReason)
	}
	if row.PickedUpAt != nil && row.DeliveredAt != nil && row.DeliveryDuration.Valid {
		d := row.DeliveryDuration
		s.Completion = &Completion{
			PickedUpAt:  row.PickedUpAt.UTC(),
			DeliveredAt: row.DeliveredAt.UTC(),
			DeliveryDuration: time.Duration(d.Microseconds)*time.Microsecond +
				time.Duration(d.Days)*24*time.Hour + time.Duration(d.Months)*30*24*time.Hour,
		}
	}
	r.Settlement = s
	return r
}

func wrap(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func optionalID(v string) *uuid.UUID {
	if v == "" {
		return nil
	}
	id := uuid.MustParse(v)
	return &id
}

func idString(v *uuid.UUID) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func utc(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}

func ptr[T any](v T) *T { return &v }

var _ Repository = (*PostgresRepository)(nil)
var _ Tx = (*transaction)(nil)
