//go:build integration

package order

import (
	"context"
	"slices"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

// The drain query must report every live obligation of a deleting account and
// never count resolved history.
func TestOutstandingWorkTruthTable(t *testing.T) {
	f := setup(t)
	t.Run("nonterminal statuses count for requester and courier", func(t *testing.T) {
		user := uuid.NewString()
		for _, status := range []Status{PendingReservation, AwaitingCourier, AwaitingPickup, Delivering, Delivered} {
			f.reset(t)
			courier := uuid.NewString()
			o := sampleOrder()
			o.Status = status
			o.RequesterID = user
			if status == AwaitingPickup || status == Delivering || status == Delivered {
				o.CourierID = &courier
			}
			f.seed(t, o)
			work, err := f.repo.OutstandingWork(f.ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(work.Reasons, OutstandingNonterminalOrder) || !work.HasOutstandingWork() {
				t.Fatalf("status %s not outstanding for requester: %+v", status, work)
			}
			if o.CourierID == nil {
				continue
			}
			work, err = f.repo.OutstandingWork(f.ctx, courier)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(work.Reasons, OutstandingNonterminalOrder) {
				t.Fatalf("status %s not outstanding for courier: %+v", status, work)
			}
		}
	})

	t.Run("terminal statuses and resolved courier history are empty", func(t *testing.T) {
		f.reset(t)
		user := uuid.NewString()
		for _, status := range []Status{Completed, Cancelled, Expired, Failed, Rejected} {
			courier := uuid.NewString()
			o := sampleOrder()
			o.Status = status
			o.RequesterID = user
			if status == Completed || status == Cancelled || status == Failed {
				o.CourierID = &courier
			}
			f.seed(t, o)
			if status == Expired || status == Rejected {
				continue
			}
			work, err := f.repo.OutstandingWork(f.ctx, courier)
			if err != nil {
				t.Fatal(err)
			}
			if work.HasOutstandingWork() {
				t.Fatalf("resolved courier history reported outstanding: %+v", work)
			}
		}
		work, err := f.repo.OutstandingWork(f.ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if work.HasOutstandingWork() {
			t.Fatalf("terminal statuses reported outstanding: %+v", work)
		}
	})

	t.Run("open and accepting offers count until voided", func(t *testing.T) {
		f.reset(t)
		user := uuid.NewString()
		order := sampleOrder()
		f.seed(t, order)
		for _, status := range []OfferStatus{OfferOpen, OfferAccepting} {
			offer := Offer{ID: uuid.NewString(), OrderID: order.ID, CourierID: user, Credits: 50,
				Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := db.New(f.pool).InsertOffer(f.ctx, db.InsertOfferParams{
				ID:        mustID(t, offer.ID),
				OrderID:   mustID(t, offer.OrderID),
				CourierID: mustID(t, offer.CourierID),
				Credits:   offer.Credits,
				Status:    string(offer.Status),
				CreatedAt: offer.CreatedAt,
				UpdatedAt: offer.UpdatedAt,
			}); err != nil {
				t.Fatal(err)
			}
			work, err := f.repo.OutstandingWork(f.ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(work.Reasons, OutstandingOpenOffer) {
				t.Fatalf("offer status %s not outstanding: %+v", status, work)
			}
			if _, err := f.pool.Exec(f.ctx, "DELETE FROM offers WHERE id = $1", mustID(t, offer.ID)); err != nil {
				t.Fatal(err)
			}
		}
		work, err := f.repo.OutstandingWork(f.ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if work.HasOutstandingWork() {
			t.Fatalf("withdrawn offers stayed outstanding: %+v", work)
		}
	})

	t.Run("pending admissions and workflows count", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			needsOffer bool
			query      string
			reason     OutstandingWorkReason
		}{
			{"creation admission", false, `INSERT INTO order_creation_workflows
				(id, order_id, requester_id, canonical_version, canonical_request, request_hash, reservation_amount,
				 expires_at, phase, lease_until, next_attempt_at, created_at, updated_at)
				VALUES ($1, $2, $3, 1, '{}', md5(random()::text) || md5(random()::text), 15,
				 now() + interval '1 hour', 'reserve_pending', now(), now(), now(), now())`,
				OutstandingPendingAdmission},
			{"assignment admission", true, `INSERT INTO order_assignment_attempts
				(id, order_id, offer_id, caller_id, courier_id, method, request_hash, amount, original_amount,
				 phase, lease_until, next_attempt_at, created_at, updated_at)
				VALUES ($1, $2, $4, $3, $3, '/order.v1.OrderService/AcceptOffer', '', 15, 15,
				 'adjustment_pending', now(), now(), now(), now())`,
				OutstandingPendingAdmission},
			{"assignment revert", true, `INSERT INTO order_assignment_attempts
				(id, order_id, offer_id, caller_id, courier_id, method, request_hash, amount, original_amount,
				 phase, lease_until, next_attempt_at, created_at, updated_at)
				VALUES ($1, $2, $4, $3, $3, '/order.v1.OrderService/AcceptOffer', '', 15, 15,
				 'revert_pending', now(), now(), now(), now())`,
				OutstandingPendingWorkflow},
			{"pending cancellation", false, `INSERT INTO order_cancellations
				(id, order_id, caller_id, reason, status, original_amount, lease_until, next_attempt_at,
				 created_at, updated_at)
				VALUES ($1, $2, $3, 'user request', 'pending', 0, now(), now(), now(), now())`,
				OutstandingPendingWorkflow},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f.reset(t)
				user := uuid.NewString()
				courier := uuid.NewString()
				o := sampleOrder()
				o.RequesterID = user
				o.Status = Completed
				o.CourierID = &courier
				f.seed(t, o)
				args := []any{uuid.New(), mustID(t, o.ID), mustID(t, user)}
				if tc.needsOffer {
					offer := Offer{ID: uuid.NewString(), OrderID: o.ID, CourierID: user, Credits: 50,
						Status: OfferVoid, CreatedAt: time.Now(), UpdatedAt: time.Now()}
					if err := db.New(f.pool).InsertOffer(f.ctx, db.InsertOfferParams{
						ID: mustID(
							t,
							offer.ID,
						),
						OrderID:   mustID(t, offer.OrderID),
						CourierID: mustID(t, offer.CourierID),
						Credits:   offer.Credits,
						Status:    string(offer.Status),
						CreatedAt: offer.CreatedAt,
						UpdatedAt: offer.UpdatedAt,
					}); err != nil {
						t.Fatal(err)
					}
					args = append(args, mustID(t, offer.ID))
				}
				if _, err := f.pool.Exec(f.ctx, tc.query, args...); err != nil {
					t.Fatal(err)
				}
				work, err := f.repo.OutstandingWork(f.ctx, user)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(work.Reasons, tc.reason) {
					t.Fatalf("%s not reported: %+v", tc.name, work)
				}
			})
		}
	})

	t.Run("unknown user is empty", func(t *testing.T) {
		f.reset(t)
		work, err := f.repo.OutstandingWork(f.ctx, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if work.HasOutstandingWork() {
			t.Fatalf("unknown user outstanding: %+v", work)
		}
	})
}

// The offer→assignment handoff must never hide both sides: the whole check is
// one statement on one snapshot, so a concurrent transition is either fully
// before or fully after it.
func TestOutstandingWorkSnapshotHandoff(t *testing.T) {
	f := setup(t)
	f.reset(t)

	courier := uuid.NewString()
	o := sampleOrder()
	f.seed(t, o)
	offer := Offer{ID: uuid.NewString(), OrderID: o.ID, CourierID: courier, Credits: 50,
		Status: OfferOpen, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.New(f.pool).InsertOffer(f.ctx, db.InsertOfferParams{
		ID: mustID(t, offer.ID), OrderID: mustID(t, offer.OrderID), CourierID: mustID(t, offer.CourierID),
		Credits: offer.Credits, Status: string(offer.Status), CreatedAt: offer.CreatedAt, UpdatedAt: offer.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}

	// Void the offer and assign the courier in one transaction, mirroring the
	// atomic offer→assignment transition, while the drain read runs concurrently.
	barrier := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(done)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(f.ctx, "UPDATE offers SET status = 'void' WHERE id = $1", mustID(t, offer.ID)); err != nil {
			done <- err
			return
		}
		if _, err = tx.Exec(f.ctx,
			"UPDATE orders SET courier_id = $2, status = 'awaiting_pickup' WHERE id = $1",
			mustID(t, o.ID), mustID(t, courier),
		); err != nil {
			done <- err
			return
		}
		barrier <- struct{}{}
		<-barrier
		done <- tx.Commit(f.ctx)
	}()
	<-barrier

	work, err := f.repo.OutstandingWork(f.ctx, courier)
	if err != nil {
		t.Fatal(err)
	}
	barrier <- struct{}{}
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	// Both sides of the transition count as outstanding; neither hides.
	if !work.HasOutstandingWork() {
		t.Fatal("handoff produced a false-empty drain result")
	}
}

func mustID(t *testing.T, id string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
