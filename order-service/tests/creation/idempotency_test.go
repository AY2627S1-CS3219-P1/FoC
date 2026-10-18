//go:build integration

package creation_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/google/uuid"
)

const makeOfferMethod = "/order.v1.OrderService/MakeOffer"

func storedKey(t *testing.T, f *fixture, scope app.IdempotencyScope, key string) *app.IdempotencyKey {
	t.Helper()
	var stored *app.IdempotencyKey
	err := f.repo.WithinCreation(f.ctx, func(tx app.CreationTx) error {
		if err := tx.LockIdempotencyCaller(f.ctx, scope); err != nil {
			return err
		}
		var err error
		stored, err = tx.FindIdempotencyKey(f.ctx, scope, key)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

type removeAfterLookup struct {
	app.CreationRepository
	once   sync.Once
	remove func() error
}

func (r *removeAfterLookup) WithinCreation(ctx context.Context, build func(app.CreationTx) error) error {
	if err := r.CreationRepository.WithinCreation(ctx, build); err != nil {
		return err
	}
	var err error
	r.once.Do(func() { err = r.remove() })
	return err
}

func TestSharedIdempotencyStorage(t *testing.T) {
	f := databaseFixture(t)
	t.Run("same key isolates callers and methods and can reference an offer", func(t *testing.T) {
		s := newScenario(t, f)
		in := input(s.clock.Now())
		c := mount(t, f, s.creator)
		first, err := create(c, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		otherCaller := uuid.NewString()
		second, err := s.creator.Create(auth.WithUserBearer(f.ctx, "Bearer user-token"), app.Caller{ID: otherCaller}, app.CreateInput{
			Key: in.IdempotencyKey, PickupID: pickupID, DeliveryID: deliveryID, Instructions: in.Delivery.Instructions,
			ItemDescription: in.ItemDescription, RewardCredits: in.RewardCredits, DeliveryDuration: in.DeliveryDuration.AsDuration(), ExpiresAt: in.ExpiresAt.AsTime(),
		})
		if err != nil {
			t.Fatal(err)
		}

		// This tests the future MakeOffer storage boundary, not an implemented RPC:
		// create an actual offer and its generic key in one PostgreSQL transaction.
		offerID := uuid.New()
		offerHash := strings.Repeat("a", 64)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		q := db.New(tx)
		if err := q.InsertOffer(f.ctx, db.InsertOfferParams{
			ID: offerID, OrderID: uuid.MustParse(second.ID), CourierID: uuid.MustParse(requester),
			Credits: 11, Status: "open", CreatedAt: s.clock.Now(), UpdatedAt: s.clock.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.InsertOrderIdempotencyKey(f.ctx, db.InsertOrderIdempotencyKeyParams{
			CallerID: uuid.MustParse(requester), Method: makeOfferMethod, IdempotencyKey: uuid.MustParse(in.IdempotencyKey),
			ResourceID: offerID, RequestHash: offerHash, ExpiresAt: s.clock.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}

		createScope := app.IdempotencyScope{CallerID: requester, Method: app.CreateOrderMethod}
		creation := storedKey(t, f, createScope, in.IdempotencyKey)
		other := storedKey(t, f, app.IdempotencyScope{CallerID: otherCaller, Method: app.CreateOrderMethod}, in.IdempotencyKey)
		offer := storedKey(t, f, app.IdempotencyScope{CallerID: requester, Method: makeOfferMethod}, in.IdempotencyKey)
		workflow, err := f.repo.Creation(f.ctx, first.Id)
		if err != nil {
			t.Fatal(err)
		}
		if creation == nil || other == nil || offer == nil || creation.ResourceID != workflow.ID || other.ResourceID == creation.ResourceID || offer.ResourceID != offerID.String() {
			t.Fatal("resource/caller/method scope was conflated")
		}
		if creation.RequestHash != workflow.Hash || offer.RequestHash != offerHash {
			t.Fatal("key row did not retain the operation payload hash")
		}
		if err := offer.CheckRequestHash(offerHash); err != nil {
			t.Fatal(err)
		}
		if err := offer.CheckRequestHash(strings.Repeat("b", 64)); !errors.Is(err, app.ErrAlreadyExists) {
			t.Fatal("changed offer payload was accepted")
		}
		replay, err := create(c, f.ctx, in)
		if err != nil || replay.Id != first.Id || f.count(t, "order_idempotency_keys") != 3 {
			t.Fatal("another method disturbed creation replay")
		}

		offerScope := app.IdempotencyScope{CallerID: requester, Method: makeOfferMethod}
		if err := f.repo.WithinCreation(f.ctx, func(tx app.CreationTx) error {
			if err := tx.LockIdempotencyCaller(f.ctx, offerScope); err != nil {
				return err
			}
			return tx.DeleteIdempotencyKey(f.ctx, offerScope, in.IdempotencyKey)
		}); err != nil {
			t.Fatal(err)
		}
		if storedKey(t, f, offerScope, in.IdempotencyKey) != nil || storedKey(t, f, createScope, in.IdempotencyKey) == nil || storedKey(t, f, app.IdempotencyScope{CallerID: otherCaller, Method: app.CreateOrderMethod}, in.IdempotencyKey) == nil {
			t.Fatal("deletion crossed caller or method scopes")
		}
	})
	t.Run("generic key insert rolls back with its resource", func(t *testing.T) {
		s := newScenario(t, f)
		order, err := create(mount(t, f, s.creator), f.ctx, input(s.clock.Now()))
		if err != nil {
			t.Fatal(err)
		}
		before := f.count(t, "order_idempotency_keys")
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		q := db.New(tx)
		offer := uuid.New()
		if err := q.InsertOffer(f.ctx, db.InsertOfferParams{ID: offer, OrderID: uuid.MustParse(order.Id), CourierID: uuid.New(), Credits: 11, Status: "open", CreatedAt: s.clock.Now(), UpdatedAt: s.clock.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := q.InsertOrderIdempotencyKey(f.ctx, db.InsertOrderIdempotencyKeyParams{CallerID: uuid.New(), Method: makeOfferMethod, IdempotencyKey: uuid.New(), ResourceID: offer, RequestHash: strings.Repeat("c", 64), ExpiresAt: s.clock.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(f.ctx); err != nil {
			t.Fatal(err)
		}
		if f.count(t, "offers") != 0 || f.count(t, "order_idempotency_keys") != before {
			t.Fatal("resource or key survived rollback")
		}
	})
	for _, expired := range []bool{false, true} {
		name := "missing resource is integrity error"
		if expired {
			name = "expired dangling key cannot be reused"
		}
		t.Run(name, func(t *testing.T) {
			s := newScenario(t, f)
			s.remotes.unknownReserve = true
			c := mount(t, f, s.creator)
			in := input(s.clock.Now())
			order, err := create(c, f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "DELETE FROM order_creation_workflows WHERE order_id=$1", order.Id); err != nil {
				t.Fatal(err)
			}
			if expired {
				if _, err := f.pool.Exec(f.ctx, "UPDATE order_idempotency_keys SET expires_at=$1", s.clock.Now().Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			s.remotes.mu.Lock()
			locations, reserves := s.remotes.locationCalls, s.remotes.reserveCalls
			s.remotes.mu.Unlock()
			if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("dangling key replay=%v", err)
			}
			if err := f.repo.WithinCreation(f.ctx, func(tx app.CreationTx) error {
				if err := tx.LockIdempotencyCaller(f.ctx, app.IdempotencyScope{CallerID: requester, Method: app.CreateOrderMethod}); err != nil {
					return err
				}
				_, err := tx.FindCreationKey(f.ctx, requester, in.IdempotencyKey, s.clock.Now())
				return err
			}); !errors.Is(err, app.ErrIdempotencyIntegrity) {
				t.Fatalf("missing workflow did not preserve integrity error: %v", err)
			}
			s.remotes.mu.Lock()
			defer s.remotes.mu.Unlock()
			if f.count(t, "orders") != 1 || f.count(t, "order_idempotency_keys") != 1 || s.remotes.locationCalls != locations || s.remotes.reserveCalls != reserves {
				t.Fatal("dangling key re-executed creation or was retired")
			}
		})
	}
	for _, resource := range []string{"orders", "order_creation_workflows"} {
		t.Run(resource+" disappearing after lookup remains an integrity error", func(t *testing.T) {
			s := newScenario(t, f)
			s.remotes.unknownReserve = true
			in := input(s.clock.Now())
			order, err := create(mount(t, f, s.creator), f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			repository := &removeAfterLookup{CreationRepository: f.repo, remove: func() error {
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(context.Background())
				if _, err := tx.Exec(f.ctx, "DELETE FROM order_creation_workflows WHERE order_id=$1", order.Id); err != nil {
					return err
				}
				if resource == "orders" {
					// Remove the aggregate's dependent rows to simulate corruption
					// after lookup while leaving its generic key intact.
					for _, statement := range []string{
						"DELETE FROM order_audit WHERE order_id=$1",
						"DELETE FROM order_outbox WHERE order_id=$1",
						"DELETE FROM orders WHERE id=$1",
					} {
						if _, err := tx.Exec(f.ctx, statement, order.Id); err != nil {
							return err
						}
					}
				}
				return tx.Commit(f.ctx)
			}}
			creator := app.NewCreator(repository, nil, nil, app.CreationOptions{Now: s.clock.Now})
			if _, err := create(mount(t, f, creator), f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("resource disappearance replay=%v", err)
			}
			if f.count(t, "order_idempotency_keys") != 1 || f.count(t, "order_creation_workflows") != 0 {
				t.Fatal("lost resource was recreated or mapping was retired")
			}
		})
	}

	t.Run("key cannot replay another caller's workflow", func(t *testing.T) {
		s := newScenario(t, f)
		in := input(s.clock.Now())
		c := mount(t, f, s.creator)
		if _, err := create(c, f.ctx, in); err != nil {
			t.Fatal(err)
		}
		otherClient := mountImages(t, f, s.creator, nil, app.Caller{ID: uuid.NewString()})
		other, err := create(otherClient, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		workflow, err := f.repo.Creation(f.ctx, other.Id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(f.ctx, "UPDATE order_idempotency_keys SET resource_id=$1 WHERE caller_id=$2 AND method=$3", workflow.ID, requester, app.CreateOrderMethod); err != nil {
			t.Fatal(err)
		}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("another caller's resource was replayed: %v", err)
		}
		if f.count(t, "orders") != 2 || f.count(t, "order_idempotency_keys") != 2 {
			t.Fatal("inconsistent resource was recreated")
		}
	})

	t.Run("inconsistent key hash is not a fresh request", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		if _, err := create(c, f.ctx, in); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(f.ctx, "UPDATE order_idempotency_keys SET request_hash=$1", strings.Repeat("d", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("inconsistent key replay=%v", err)
		}
		if f.count(t, "orders") != 1 || f.count(t, "order_idempotency_keys") != 1 {
			t.Fatal("inconsistent key was reused")
		}
	})
	t.Run("failed shared-key write poisons transaction even if swallowed", func(t *testing.T) {
		s := newScenario(t, f)
		scope := app.IdempotencyScope{CallerID: requester, Method: makeOfferMethod}
		err := f.repo.WithinCreation(f.ctx, func(tx app.CreationTx) error {
			if err := tx.LockIdempotencyCaller(f.ctx, scope); err != nil {
				return err
			}
			_ = tx.InsertIdempotencyKey(f.ctx, app.IdempotencyKey{Scope: scope, Key: uuid.NewString(), ResourceID: uuid.NewString(), RequestHash: "invalid", ExpiresAt: s.clock.Now().Add(time.Hour)})
			return nil
		})
		if !errors.Is(err, app.ErrPrecondition) || f.count(t, "order_idempotency_keys") != 0 {
			t.Fatalf("invalid shared-key write committed: %v", err)
		}
	})
}

func TestSharedIdempotencySchema(t *testing.T) {
	f := databaseFixture(t)
	var nullable string
	if err := f.pool.QueryRow(f.ctx, "SELECT is_nullable FROM information_schema.columns WHERE table_name='order_idempotency_keys' AND column_name='request_hash'").Scan(&nullable); err != nil || nullable != "NO" {
		t.Fatal("request hash is nullable")
	}
	var foreignKeys int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='order_idempotency_keys'::regclass AND contype='f'").Scan(&foreignKeys); err != nil || foreignKeys != 0 {
		t.Fatal("generic resource still has a foreign key")
	}
	var primaryKey []string
	if err := f.pool.QueryRow(f.ctx, `SELECT array_agg(a.attname::text ORDER BY position) FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS columns(attnum,position) JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=columns.attnum WHERE c.conrelid='order_idempotency_keys'::regclass AND c.contype='p'`).Scan(&primaryKey); err != nil || strings.Join(primaryKey, ",") != "caller_id,method,idempotency_key" {
		t.Fatalf("primary key changed: %v %v", primaryKey, err)
	}
}
