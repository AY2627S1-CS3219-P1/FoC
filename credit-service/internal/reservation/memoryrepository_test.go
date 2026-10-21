package reservation_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
)

// memoryRepository commits a unit of work only when it succeeds, and rejects
// the changes the database constraints and triggers reject.
type memoryRepository struct{ state memoryState }

type memoryState struct {
	accounts     map[string]account.Account
	ledger       []reservation.Entry
	reservations map[string]reservation.Reservation
	revisions    map[string]reservation.Reservation
	assignments  map[string]reservation.Assignment
	operations   map[reservation.OperationKey]reservation.Operation
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{state: memoryState{
		accounts:     map[string]account.Account{},
		reservations: map[string]reservation.Reservation{},
		revisions:    map[string]reservation.Reservation{},
		assignments:  map[string]reservation.Assignment{},
		operations:   map[reservation.OperationKey]reservation.Operation{},
	}}
}

func (m *memoryRepository) Within(_ context.Context, f func(reservation.Tx) error) error {
	s := memoryState{
		accounts:     maps.Clone(m.state.accounts),
		ledger:       append([]reservation.Entry(nil), m.state.ledger...),
		reservations: maps.Clone(m.state.reservations),
		revisions:    maps.Clone(m.state.revisions),
		assignments:  maps.Clone(m.state.assignments),
		operations:   maps.Clone(m.state.operations),
	}
	if err := f(&memoryTx{&s}); err != nil {
		return err
	}
	m.state = s
	return nil
}

type memoryTx struct{ s *memoryState }

var errConstraint = errors.New("constraint violation")

func revisionKey(orderID string, revision int64) string {
	return fmt.Sprintf("%s/%d", orderID, revision)
}
func assignmentKey(orderID, assignmentID string) string { return orderID + "/" + assignmentID }

func (t *memoryTx) LockOrder(context.Context, string) error { return nil }

func (t *memoryTx) Operation(_ context.Context, key reservation.OperationKey) (*reservation.Operation, error) {
	op, ok := t.s.operations[key]
	if !ok {
		return nil, nil
	}
	return &op, nil
}

func (t *memoryTx) SaveOperation(_ context.Context, key reservation.OperationKey, op reservation.Operation) error {
	if _, ok := t.s.operations[key]; ok {
		return errConstraint
	}
	if !op.Insufficient {
		if _, ok := t.s.revisions[revisionKey(key.OrderID, op.Revision)]; !ok {
			return errConstraint
		}
	}
	t.s.operations[key] = op
	return nil
}

func (t *memoryTx) Reservation(_ context.Context, orderID string) (*reservation.Reservation, error) {
	r, ok := t.s.reservations[orderID]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (t *memoryTx) Revision(_ context.Context, orderID string, revision int64) (reservation.Reservation, error) {
	r, ok := t.s.revisions[revisionKey(orderID, revision)]
	if !ok {
		return reservation.Reservation{}, errors.New("no revision")
	}
	return r, nil
}

func (t *memoryTx) save(r reservation.Reservation) error {
	if r.Adjustment != nil {
		return errors.New("adjustment is not stored")
	}
	if (r.Status == reservation.Released || r.Status == reservation.Settled) != (r.Settlement != nil) {
		return errConstraint
	}
	if r.Status == reservation.Held && (r.ReservedAmount != r.OriginalAmount || r.AssignmentID != "") {
		return errConstraint
	}
	t.s.reservations[r.OrderID] = r
	t.s.revisions[revisionKey(r.OrderID, r.Revision)] = r
	return nil
}

func (t *memoryTx) InsertReservation(_ context.Context, r reservation.Reservation) (reservation.Reservation, error) {
	if _, ok := t.s.reservations[r.OrderID]; ok {
		return reservation.Reservation{}, errConstraint
	}
	return r, t.save(r)
}

// UpdateReservation assigns the revision and updated_at, as the bump_revision
// trigger does.
func (t *memoryTx) UpdateReservation(_ context.Context, r reservation.Reservation, expected int64) (reservation.Reservation, error) {
	old, ok := t.s.reservations[r.OrderID]
	if !ok || old.Revision != expected {
		return reservation.Reservation{}, reservation.ErrAborted
	}
	if old.Status == reservation.Released || old.Status == reservation.Settled {
		return reservation.Reservation{}, errors.New("terminal reservation cannot change")
	}
	r.Revision = old.Revision + 1
	r.UpdatedAt = time.Now().UTC()
	return r, t.save(r)
}

func (t *memoryTx) Assignment(_ context.Context, orderID, assignmentID string) (*reservation.Assignment, error) {
	a, ok := t.s.assignments[assignmentKey(orderID, assignmentID)]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func (t *memoryTx) InsertAssignment(_ context.Context, a reservation.Assignment) error {
	if _, ok := t.s.reservations[a.OrderID]; !ok {
		return errConstraint
	}
	if _, ok := t.s.assignments[assignmentKey(a.OrderID, a.AssignmentID)]; ok {
		return errConstraint
	}
	for _, other := range t.s.assignments {
		if a.Status == reservation.Applied && other.OrderID == a.OrderID && other.Status == reservation.Applied {
			return errConstraint
		}
	}
	t.s.assignments[assignmentKey(a.OrderID, a.AssignmentID)] = a
	return nil
}

func (t *memoryTx) RevertAssignment(_ context.Context, orderID, assignmentID string, at time.Time) error {
	k := assignmentKey(orderID, assignmentID)
	a, ok := t.s.assignments[k]
	if !ok || a.Status != reservation.Applied {
		return reservation.ErrAborted
	}
	a.Status, a.RevertedAt = reservation.Reverted, &at
	t.s.assignments[k] = a
	return nil
}

func (t *memoryTx) LockAccounts(_ context.Context, at time.Time, ids ...string) (map[string]account.Account, error) {
	out := map[string]account.Account{}
	for _, id := range ids {
		if _, ok := t.s.accounts[id]; !ok {
			t.s.accounts[id] = account.Account{UserID: id}
			if err := t.Post(context.Background(), at, reservation.Entry{UserID: id, Kind: account.Grant, BalanceDelta: account.StartingCredits}); err != nil {
				return nil, err
			}
		}
		out[id] = t.s.accounts[id]
	}
	return out, nil
}

func (t *memoryTx) Post(_ context.Context, _ time.Time, entries ...reservation.Entry) error {
	for _, e := range entries {
		if e.BalanceDelta == 0 && e.ReservedDelta == 0 {
			continue
		}
		a, ok := t.s.accounts[e.UserID]
		if !ok {
			return errConstraint
		}
		a.Balance += e.BalanceDelta
		a.Reserved += e.ReservedDelta
		if a.Reserved < 0 || a.Available() < 0 {
			return fmt.Errorf("%w: account %s balance %d reserved %d", errConstraint, e.UserID, a.Balance, a.Reserved)
		}
		t.s.accounts[e.UserID] = a
		t.s.ledger = append(t.s.ledger, e)
	}
	return nil
}

// checkLedger verifies that every account equals the sum of its ledger and
// that reserved credits equal the requester's active holds.
func (m *memoryRepository) checkLedger(t *testing.T) {
	t.Helper()
	balance, reserved, held := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, e := range m.state.ledger {
		balance[e.UserID] += e.BalanceDelta
		reserved[e.UserID] += e.ReservedDelta
	}
	for _, r := range m.state.reservations {
		held[r.RequesterID] += r.ReservedAmount
	}
	for id, a := range m.state.accounts {
		if a.Balance != balance[id] || a.Reserved != reserved[id] || a.Reserved != held[id] || a.Available() < 0 {
			t.Fatalf("account %s = %+v; ledger balance %d reserved %d; holds %d", id, a, balance[id], reserved[id], held[id])
		}
	}
}

func (m *memoryRepository) balance(id string) account.Account { return m.state.accounts[id] }

var _ reservation.Repository = (*memoryRepository)(nil)
