//go:build integration

package creation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/google/uuid"
)

type failLocalCommit struct {
	app.CreationRepository
	armed  atomic.Bool
	status app.Status
}

type failApply struct {
	app.CreationTx
	repository *failLocalCommit
}

func (r *failLocalCommit) WithinCreation(ctx context.Context, build func(app.CreationTx) error) error {
	return r.CreationRepository.WithinCreation(
		ctx,
		func(tx app.CreationTx) error { return build(failApply{CreationTx: tx, repository: r}) },
	)
}

func (t failApply) Apply(ctx context.Context, c app.Change) error {
	if err := t.CreationTx.Apply(ctx, c); err != nil {
		return err
	}
	if c.Order.Status == t.repository.status && t.repository.armed.CompareAndSwap(true, false) {
		return errors.New("injected failure after aggregate writes before commit")
	}
	return nil
}

func TestCreationCrashRecovery(t *testing.T) {
	f := databaseFixture(t)
	t.Run("remote Reserve commit survives local transaction rollback and restart", func(t *testing.T) {
		s := newScenario(t, f)
		broken := &failLocalCommit{CreationRepository: f.repo, status: app.AwaitingCourier}
		broken.armed.Store(true)
		creator := app.NewCreator(
			broken,
			s.locations,
			s.credit,
			app.CreationOptions{AdmissionsEnabled: true, Now: s.clock.Now},
		)
		c := mount(t, f, creator)
		in := input(s.clock.Now())
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("injected failure=%v", err)
		}
		id := f.onlyOrder(t)
		if get(t, c, id).Status != orderpb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION ||
			f.count(t, "order_audit") != 1 {
			t.Fatal("partial local confirmation committed")
		}
		s.clock.Advance(16 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		replay, err := create(mount(t, f, s.restart()), f.ctx, in)
		if err != nil || replay.Id != id || replay.Status != orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER ||
			f.count(t, "order_audit") != 2 {
			t.Fatalf("recovery=%v err=%v", replay, err)
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.reserveCalls != 2 || s.remotes.holds != 1 {
			t.Fatal("recovery changed remote operation identity")
		}
	})
	t.Run("remote Release commit survives local rejection rollback and restart", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownReserve = true
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		pending, err := create(c, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		s.clock.Advance(2 * time.Hour)
		broken := &failLocalCommit{CreationRepository: f.repo, status: app.Rejected}
		broken.armed.Store(true)
		firstRecovery := app.NewCreator(broken, nil, s.credit, app.CreationOptions{Now: s.clock.Now})
		if err := firstRecovery.RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		w, err := f.repo.Creation(f.ctx, pending.Id)
		if err != nil || w.Phase != app.AbortPending ||
			get(t, c, pending.Id).Status != orderpb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION {
			t.Fatalf("abort durability=%+v err=%v", w, err)
		}
		s.clock.Advance(16 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		w, err = f.repo.Creation(f.ctx, pending.Id)
		if err != nil || w.Rejection != app.CreationExpired || f.count(t, "order_audit") != 2 {
			t.Fatalf("release recovery=%+v err=%v", w, err)
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.releaseCalls != 2 || s.remotes.reserveCalls != 1 {
			t.Fatal("recovery dispatched wrong financial operation")
		}
	})
	t.Run("admission failure rolls back Order mapping audit and facts", func(t *testing.T) {
		s := newScenario(t, f)
		_, err := f.pool.Exec(
			f.ctx,
			`CREATE FUNCTION fail_creation_mapping() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected admission failure'; END; $$; CREATE TRIGGER fail_creation_mapping BEFORE INSERT ON order_idempotency_keys FOR EACH ROW EXECUTE FUNCTION fail_creation_mapping();`,
		)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.pool.Exec(
				f.ctx,
				"DROP TRIGGER fail_creation_mapping ON order_idempotency_keys; DROP FUNCTION fail_creation_mapping()",
			); err != nil {
				t.Error(err)
			}
		}()
		if _, err := create(
			mount(t, f, s.creator),
			f.ctx,
			input(s.clock.Now()),
		); connect.CodeOf(
			err,
		) != connect.CodeInternal {
			t.Fatalf("admission failure=%v", err)
		}
		for _, table := range []string{"orders", "order_creation_workflows", "order_idempotency_keys", "order_audit", "order_outbox"} {
			if f.count(t, table) != 0 {
				t.Fatalf("partial admission in %s", table)
			}
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.reserveCalls != 0 {
			t.Fatal("remote call before durable admission")
		}
	})
	t.Run("workflow immutable payload and write failures poison transaction", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownReserve = true
		pending, err := create(mount(t, f, s.creator), f.ctx, input(s.clock.Now()))
		if err != nil {
			t.Fatal(err)
		}
		err = f.repo.WithinCreation(f.ctx, func(tx app.CreationTx) error {
			if _, err := tx.LockOrder(f.ctx, pending.Id); err != nil {
				return err
			}
			w, err := tx.LockCreation(f.ctx, pending.Id)
			if err != nil {
				return err
			}
			w.Canonical[0] = '['
			_ = tx.SaveCreation(f.ctx, w, w.Generation) // Even swallowed failures must abort.
			return nil
		})
		if !errors.Is(err, app.ErrConflict) {
			t.Fatalf("mutated workflow committed: %v", err)
		}
		w, err := f.repo.Creation(f.ctx, pending.Id)
		if err != nil || w.Canonical[0] != '{' {
			t.Fatalf("immutable payload damaged: %v", err)
		}
	})
}

func TestCreationReconcilesUnknownOrMismatchedResponses(t *testing.T) {
	f := databaseFixture(t)
	t.Run("lost Reserve response reconciles current hold", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownAfterReserve = true
		o, err := create(mount(t, f, s.creator), f.ctx, input(s.clock.Now()))
		if err != nil || o.Status != orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
			t.Fatalf("reconciliation=%v err=%v", o, err)
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.holds != 1 || s.remotes.reserveCalls != 1 {
			t.Fatal("unknown response duplicated Reserve")
		}
	})
	t.Run("current hold must match the persisted financial identity", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.onGet = func(v *creditpb.Reservation) { v.RequesterId = uuid.NewString() }
		c := mount(t, f, s.creator)
		o, err := create(c, f.ctx, input(s.clock.Now()))
		if err != nil || o.Status != orderpb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION {
			t.Fatalf("mismatched proof=%v err=%v", o, err)
		}
		w, err := f.repo.Creation(f.ctx, o.Id)
		if err != nil || w.LastError != "reservation_mismatch" {
			t.Fatalf("mismatch state=%+v err=%v", w, err)
		}
		s.remotes.mu.Lock()
		s.remotes.onGet = nil
		s.remotes.mu.Unlock()
		s.clock.Advance(2 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		if get(t, c, o.Id).Status != orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
			t.Fatal("matching proof did not recover")
		}
	})
}
