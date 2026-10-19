//go:build integration

package order

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type fixture struct {
	ctx  context.Context
	pool *pgxpool.Pool
	repo *PostgresRepository
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		container, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("order_test"), postgres.WithUsername("order"), postgres.WithPassword("order"), postgres.BasicWaitStrategies())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := container.Terminate(context.Background()); err != nil {
				t.Error(err)
			}
		})
		url, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version int
	if err = pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil || version < 180000 || version >= 190000 {
		t.Fatalf("requires disposable PostgreSQL 18: %d %v", version, err)
	}
	// TEST_DATABASE_URL must point at a throwaway database: this resets its schema.
	if _, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqldb, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{ctx, pool, NewPostgresRepository(pool)}
}
func (f *fixture) reset(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, "TRUNCATE order_outbox,order_audit,offers,orders CASCADE"); err != nil {
		t.Fatal(err)
	}
}
func sampleOrder() Order {
	now := time.Now().UTC().Truncate(time.Microsecond)
	instructions := "Room 7: call on arrival"
	floor := "2"
	return Order{ID: uuid.NewString(), RequesterID: uuid.NewString(), Status: PendingReservation, Pickup: LocationSnapshot{LocationID: uuid.NewString(), Name: "Café", BuildingName: "COM2", Floor: &floor}, Delivery: LocationSnapshot{LocationID: uuid.NewString(), Name: "Lobby", BuildingName: "PGP"}, DeliveryInstructions: &instructions, ItemDescription: "Lunch", RewardCredits: 15, ReservedCredits: 15, DeliveryDuration: 15*time.Minute + 1234, ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now}
}
func (f *fixture) create(t *testing.T) Order {
	t.Helper()
	o := sampleOrder()
	c, err := NewChange(nil, o, Actor{User, o.RequesterID}, Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Within(f.ctx, func(tx Tx) error { return tx.Apply(f.ctx, c) }); err != nil {
		t.Fatal(err)
	}
	return o
}
func (f *fixture) transition(id string, a Action, actor Actor) error {
	return NewMutator(f.repo).Mutate(f.ctx, id, func(before Order, _ []Offer) (*Change, error) {
		status, repeat, err := Transition(before, a, actor)
		if err != nil {
			return nil, err
		}
		if repeat {
			return nil, nil
		}
		after := before
		after.Status = status
		after.Revision++
		after.UpdatedAt = after.UpdatedAt.Add(time.Second)
		switch a {
		case Assign:
			after.CourierID = &actor.ID
			after.AssignedAt = &after.UpdatedAt
		case Pickup:
			after.PickedUpAt = &after.UpdatedAt
		case Deliver:
			after.DeliveredAt = &after.UpdatedAt
		case ConfirmReceipt:
			after.CompletedAt = &after.UpdatedAt
		case Withdraw, Unassign:
			after.CourierID = nil
			after.AssignedAt = nil
		}
		if status.IsTerminal() && status != Completed {
			after.ClosedAt = &after.UpdatedAt
		}
		c, e := NewChange(&before, after, actor, a, nil, uuid.NewString)
		return &c, e
	})
}
func (f *fixture) mustTransition(t *testing.T, id string, a Action, actor Actor) {
	t.Helper()
	if err := f.transition(id, a, actor); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) counts(t *testing.T, id string) (audit, outbox, offers int) {
	t.Helper()
	for _, v := range []struct {
		table  string
		target *int
	}{{"order_audit", &audit}, {"order_outbox", &outbox}, {"offers", &offers}} {
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM "+v.table+" WHERE order_id=$1", id).Scan(v.target); err != nil {
			t.Fatal(err)
		}
	}
	return
}
func TestPostgresFoundation(t *testing.T) {
	f := setup(t)
	t.Run("migration round trip and complete snapshots", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(o, got) {
			t.Fatalf("round trip mismatch\n%+v\n%+v", o, got)
		}
		a, n, _ := f.counts(t, o.ID)
		if a != 1 || n != 1 {
			t.Fatal(a, n)
		}
		var payload []byte
		if err = f.pool.QueryRow(f.ctx, "SELECT payload FROM order_outbox WHERE order_id=$1", o.ID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var fact FactsPayload
		if err = json.Unmarshal(payload, &fact); err != nil || fact.Status != PendingReservation || fact.ReservedCredits != 15 {
			t.Fatal(string(payload), err)
		}
	})
	// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): a receipt repeat leaves the
	// committed revision and audit/outbox counts unchanged.
	t.Run("full completion facts and recognized repeats", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		courier := Actor{User, uuid.NewString()}
		for _, step := range []struct {
			a     Action
			actor Actor
		}{{ConfirmReservation, Actor{Kind: System}}, {Assign, courier}, {Pickup, courier}, {Deliver, courier}, {ConfirmReceipt, Actor{User, o.RequesterID}}} {
			f.mustTransition(t, o.ID, step.a, step.actor)
		}
		before, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.mustTransition(t, o.ID, ConfirmReceipt, Actor{User, o.RequesterID})
		after, _ := f.repo.Get(f.ctx, o.ID)
		if before.Revision != after.Revision {
			t.Fatal("repeat wrote")
		}
		a, n, _ := f.counts(t, o.ID)
		if a != 6 || n != 7 {
			t.Fatal(a, n)
		}
		var payload []byte
		if err = f.pool.QueryRow(f.ctx, "SELECT payload FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var outcome FactsPayload
		if err = json.Unmarshal(payload, &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome.RequesterID != o.RequesterID || outcome.CourierID == nil || *outcome.CourierID != courier.ID || !outcome.AfterPickup || outcome.PickedUpAt == nil || outcome.DeliveredAt == nil || outcome.ReservedCredits != 15 || outcome.DeliveryDurationNS != int64(o.DeliveryDuration) {
			t.Fatalf("incomplete facts: %+v", outcome)
		}
		rows, err := f.pool.Query(f.ctx, "SELECT revision,ordinal,kind FROM order_outbox WHERE order_id=$1 ORDER BY revision,ordinal", o.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var last int64
		var ordinal int32 = -1
		for rows.Next() {
			var rev int64
			var ord int32
			var kind string
			if err = rows.Scan(&rev, &ord, &kind); err != nil {
				t.Fatal(err)
			}
			if rev == last {
				if ord != ordinal+1 {
					t.Fatal(rev, ord)
				}
			} else if ord != 0 {
				t.Fatal(rev, ord)
			}
			last, ordinal = rev, ord
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancellation facts and warning then terminal metadata", func(t *testing.T) {
		for _, afterPickup := range []bool{false, true} {
			for _, byCourier := range []bool{false, true} {
				if !afterPickup && byCourier {
					continue
				}
				f.reset(t)
				o := f.create(t)
				courier := Actor{User, uuid.NewString()}
				f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
				f.mustTransition(t, o.ID, Assign, courier)
				if afterPickup {
					f.mustTransition(t, o.ID, Pickup, courier)
				}
				actor := Actor{User, o.RequesterID}
				if byCourier {
					actor = courier
				}
				f.mustTransition(t, o.ID, Cancel, actor)
				var payload []byte
				if err := f.pool.QueryRow(f.ctx, "SELECT payload FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				var fact FactsPayload
				if err := json.Unmarshal(payload, &fact); err != nil {
					t.Fatal(err)
				}
				if fact.AfterPickup != afterPickup {
					t.Fatal(string(payload))
				}
				var warnings int
				if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.courier_warning_requested'", o.ID).Scan(&warnings); err != nil {
					t.Fatal(err)
				}
				if (warnings == 1) != byCourier {
					t.Fatal(warnings, byCourier)
				}
				if afterPickup {
					_, events, _ := f.counts(t, o.ID)
					err := NewMutator(f.repo).Mutate(f.ctx, o.ID, func(before Order, _ []Offer) (*Change, error) {
						after := before
						after.Revision++
						after.UpdatedAt = before.UpdatedAt.Add(time.Second)
						where := "reception"
						after.ItemLocation = &where
						c, e := NewChange(&before, after, courier, RecordItemLocation, nil, uuid.NewString)
						return &c, e
					})
					if err != nil {
						t.Fatal(err)
					}
					_, nowEvents, _ := f.counts(t, o.ID)
					if events != nowEvents {
						t.Fatal("metadata emitted status/outcome")
					}
				}
			}
		}
	})
	t.Run("rejection has no settlement outcome", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, RefuseReservation, Actor{Kind: System})
		a, n, _ := f.counts(t, o.ID)
		if a != 2 || n != 2 {
			t.Fatal(a, n)
		}
	})
	// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): stale local changes cannot
	// advance the aggregate or replay the transaction callback automatically.
	t.Run("revision conflicts and no automatic callback retry", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		calls := 0
		err := f.repo.Within(f.ctx, func(tx Tx) error {
			calls++
			before, e := tx.LockOrder(f.ctx, o.ID)
			if e != nil {
				return e
			}
			if _, e = tx.Relationships(f.ctx, o.ID); e != nil {
				return e
			}
			after := before
			after.Status = AwaitingCourier
			after.Revision++
			after.UpdatedAt = after.UpdatedAt.Add(time.Second)
			c, e := NewChange(&before, after, Actor{Kind: System}, ConfirmReservation, nil, uuid.NewString)
			if e != nil {
				return e
			}
			c.ExpectedRevision--
			return tx.Apply(f.ctx, c)
		})
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatal(calls)
		}
		got, _ := f.repo.Get(f.ctx, o.ID)
		if got.Revision != 1 {
			t.Fatal(got)
		}
	})
	// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): stale revision predicates
	// affect no rows, preventing duplicate aggregate changes.
	t.Run("conditional SQL revision predicate", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		row, err := toOrder(o)
		if err != nil {
			t.Fatal(err)
		}
		n, err := db.New(f.pool).UpdateOrder(f.ctx, db.UpdateOrderParams{ID: row.ID, CourierID: row.CourierID, Status: row.Status, Pickup: row.Pickup, Delivery: row.Delivery, DeliveryInstructions: row.DeliveryInstructions, ItemDescription: row.ItemDescription, RewardCredits: row.RewardCredits, ReservedCredits: row.ReservedCredits, DeliveryDurationNs: row.DeliveryDurationNs, ExpiresAt: row.ExpiresAt, Revision: 2, UpdatedAt: row.UpdatedAt, ExpectedRevision: 99})
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
	})
	// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): a failed fact insertion
	// leaves no partial aggregate, offer, audit or outgoing fact.
	t.Run("atomic failure rolls back Order offers audit and outbox", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		beforeA, beforeN, _ := f.counts(t, o.ID)
		var existing string
		if err := f.pool.QueryRow(f.ctx, "SELECT event_id FROM order_outbox LIMIT 1").Scan(&existing); err != nil {
			t.Fatal(err)
		}
		err := NewMutator(f.repo).Mutate(f.ctx, o.ID, func(before Order, _ []Offer) (*Change, error) {
			after := before
			after.Status = Cancelled
			after.Revision++
			after.UpdatedAt = before.UpdatedAt.Add(time.Second)
			c, e := NewChange(&before, after, Actor{User, o.RequesterID}, Cancel, nil, uuid.NewString)
			if e != nil {
				return nil, e
			}
			c.Facts[1].ID = existing
			c.Offers = []OfferChange{{Offer: Offer{ID: uuid.NewString(), OrderID: o.ID, CourierID: uuid.NewString(), Credits: 20, Status: OfferOpen, CreatedAt: after.UpdatedAt, UpdatedAt: after.UpdatedAt}}}
			return &c, nil
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		got, _ := f.repo.Get(f.ctx, o.ID)
		a, n, offers := f.counts(t, o.ID)
		if got.Status != AwaitingCourier || got.Revision != 2 || a != beforeA || n != beforeN || offers != 0 {
			t.Fatal(got, a, n, offers)
		}
	})
	// NFR-02/NFR-11.6 (../../REQUIREMENTS.md#nfr-02): partial foundation coverage
	// with 16 local assignment mutations; this is not the 50-acceptance RPC test.
	t.Run("competing mutations serialize to one courier", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				if err := f.transition(o.ID, Assign, Actor{User, uuid.NewString()}); err == nil {
					wins.Add(1)
				} else if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrPrecondition) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal(wins.Load())
		}
		a, n, _ := f.counts(t, o.ID)
		if a != 3 || n != 3 {
			t.Fatal(a, n)
		}
	})
	// NFR-02/NFR-11.6 (../../REQUIREMENTS.md#nfr-02): the local assignment/expiry
	// race commits one change; acceptance RPC/saga concurrency remains deferred.
	t.Run("assignment racing expiry commits one change", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		var wins atomic.Int32
		var wg sync.WaitGroup
		for _, fn := range []func() error{func() error { return f.transition(o.ID, Assign, Actor{User, uuid.NewString()}) }, func() error { return f.transition(o.ID, Expire, Actor{Kind: System}) }} {
			wg.Go(func() {
				if fn() == nil {
					wins.Add(1)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal(wins.Load())
		}
	})
	t.Run("relationships use statement after waiting on parent lock", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(f.ctx, tx)
		if _, err = tx.Exec(f.ctx, "UPDATE orders SET revision=revision+1 WHERE id=$1", o.ID); err != nil {
			t.Fatal(err)
		}
		offerID := uuid.NewString()
		if _, err = tx.Exec(f.ctx, "INSERT INTO offers VALUES($1,$2,$3,20,'open',now(),now())", offerID, o.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			result <- f.repo.Within(f.ctx, func(t Tx) error {
				current, e := t.LockOrder(f.ctx, o.ID)
				if e != nil {
					return e
				}
				offers, e := t.Relationships(f.ctx, o.ID)
				if e != nil {
					return e
				}
				if current.Revision != 2 || len(offers) != 1 || offers[0].ID != offerID {
					return fmt.Errorf("stale snapshot: %+v %+v", current, offers)
				}
				return nil
			})
		}()
		deadline := time.Now().Add(5 * time.Second)
		blocked := false
		for time.Now().Before(deadline) {
			if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM orders%FOR UPDATE%')").Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !blocked {
			t.Fatal("reader never waited for parent lock")
		}
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-result; err != nil {
			t.Fatal(err)
		}
	})
	// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): database constraints reject
	// duplicate revision facts and terminal outcomes; delivery/replay is untested.
	t.Run("outbox uniqueness protects revisions and terminal outcomes", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		f.mustTransition(t, o.ID, Cancel, Actor{User, o.RequesterID})
		for _, q := range []string{
			"INSERT INTO order_outbox SELECT gen_random_uuid(),order_id,revision,ordinal+10,kind,payload_version,payload,created_at,NULL FROM order_outbox WHERE order_id=$1 AND kind='order.status_changed' LIMIT 1",
			"INSERT INTO order_outbox SELECT gen_random_uuid(),order_id,revision+1,ordinal,kind,payload_version,payload,created_at,NULL FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'",
		} {
			if _, err := f.pool.Exec(f.ctx, q, o.ID); err == nil {
				t.Fatal("duplicate fact accepted")
			}
		}
		// Historical accepted offers can coexist through reassignment.
		for range 2 {
			if _, err := f.pool.Exec(f.ctx, "INSERT INTO offers VALUES($1,$2,$3,20,'accepted',now(),now())", uuid.NewString(), o.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("read only snapshot stays consistent without blocking writers", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		if err := f.repo.snapshot(f.ctx, func(q *db.Queries) error {
			before, e := q.GetOrder(f.ctx, uuid.MustParse(o.ID))
			if e != nil {
				return e
			}
			if _, e = f.pool.Exec(f.ctx, "UPDATE orders SET revision=revision+1 WHERE id=$1", o.ID); e != nil {
				return e
			}
			after, e := q.GetOrder(f.ctx, uuid.MustParse(o.ID))
			if e == nil && before.Revision != after.Revision {
				return errors.New("read snapshot changed")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
	})
}
