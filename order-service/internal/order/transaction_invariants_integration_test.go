//go:build integration

package order

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresTransactionInvariants(t *testing.T) {
	f := setup(t)
	t.Run("uppercase resource id follows lock and relationship path", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		if err := f.transition(strings.ToUpper(o.ID), ConfirmReservation, Actor{Kind: System}); err != nil {
			t.Fatal(err)
		}
	})
	// NFR-05.2.3/NFR-05.2.5 (../../REQUIREMENTS.md#nfr-05-auth): mutating a
	// returned pointer cannot replace the persisted courier used for authorization.
	t.Run("locked authorization baseline cannot be aliased", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		courier := Actor{User, uuid.NewString()}
		f.mustTransition(t, o.ID, Assign, courier)
		impostor := Actor{User, uuid.NewString()}
		err := f.repo.Within(f.ctx, func(tx Tx) error {
			before, err := tx.LockOrder(f.ctx, o.ID)
			if err != nil {
				return err
			}
			if _, err = tx.Relationships(f.ctx, o.ID); err != nil {
				return err
			}
			*before.CourierID = impostor.ID
			after := before
			after.Status = Delivering
			after.Revision++
			after.UpdatedAt = after.UpdatedAt.Add(time.Second)
			c, err := NewChange(&before, after, impostor, Pickup, nil, uuid.NewString)
			if err != nil {
				return err
			}
			return tx.Apply(f.ctx, c)
		})
		if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		got, _ := f.repo.Get(f.ctx, o.ID)
		if got.CourierID == nil || *got.CourierID != courier.ID || got.Status != AwaitingPickup {
			t.Fatal(got)
		}
	})
	t.Run("swallowed apply failure cannot commit partial state", func(t *testing.T) {
		f.reset(t)
		o := f.create(t)
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		err := f.repo.Within(f.ctx, func(tx Tx) error {
			before, err := tx.LockOrder(f.ctx, o.ID)
			if err != nil {
				return err
			}
			if _, err = tx.Relationships(f.ctx, o.ID); err != nil {
				return err
			}
			after := before
			after.Status = Cancelled
			after.Revision++
			after.UpdatedAt = after.UpdatedAt.Add(time.Second)
			c, err := NewChange(&before, after, Actor{User, o.RequesterID}, Cancel, nil, uuid.NewString)
			if err != nil {
				return err
			}
			c.Offers = []OfferChange{{Offer: Offer{ID: uuid.NewString(), OrderID: uuid.NewString(), CourierID: uuid.NewString(), Credits: 20, Status: OfferOpen}}}
			_ = tx.Apply(f.ctx, c)
			return nil
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		got, _ := f.repo.Get(f.ctx, o.ID)
		a, n, _ := f.counts(t, o.ID)
		if got.Status != AwaitingCourier || got.Revision != 2 || a != 2 || n != 2 {
			t.Fatal(got, a, n)
		}
	})
}
