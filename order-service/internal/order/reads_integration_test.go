//go:build integration

package order

import (
	"errors"
	"strings"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

func (f *fixture) seed(t *testing.T, o Order) {
	t.Helper()
	row, err := toOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.New(f.pool).InsertOrder(f.ctx, db.InsertOrderParams(row)); err != nil {
		t.Fatal(err)
	}
}
func TestPostgresReads(t *testing.T) {
	f := setup(t)
	service := NewService(f.repo)
	t.Run("visibility open eligibility and pagination", func(t *testing.T) {
		f.reset(t)
		caller := Caller{ID: uuid.NewString()}
		requester := uuid.NewString()
		pickup, delivery := uuid.NewString(), uuid.NewString()
		now := time.Now().UTC().Truncate(time.Microsecond)
		openIDs := []string{}
		for _, status := range []Status{PendingReservation, AwaitingCourier, AwaitingCourier, AwaitingCourier, AwaitingPickup, Delivering, Delivered, Completed, Cancelled, Expired, Failed, Rejected} {
			o := sampleOrder()
			o.RequesterID = requester
			o.Status = status
			o.CreatedAt = now
			o.UpdatedAt = now
			o.Pickup.LocationID = pickup
			o.Delivery.LocationID = delivery
			if status == AwaitingPickup || status == Delivering || status == Delivered || status == Completed {
				courier := uuid.NewString()
				o.CourierID = &courier
			}
			f.seed(t, o)
			if status == AwaitingCourier {
				openIDs = append(openIDs, o.ID)
			}
		}
		own := sampleOrder()
		own.RequesterID = caller.ID
		own.Status = AwaitingCourier
		f.seed(t, own)
		expired := sampleOrder()
		expired.Status = AwaitingCourier
		expired.ExpiresAt = time.Now().Add(-time.Second)
		f.seed(t, expired)
		p, err := service.ListOpenOrders(f.ctx, caller, ListRequest{Page: Page{Size: 2}, PickupID: strings.ToUpper(pickup), DeliveryID: strings.ToUpper(delivery)})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Orders) != 2 || p.PageInfo.Total != 3 || p.PageInfo.Size != 2 || p.PageInfo.Number != 1 || p.PageInfo.TotalPages() != 2 {
			t.Fatalf("page %+v", p)
		}
		for _, o := range p.Orders {
			if o.Status != AwaitingCourier || o.RequesterID == caller.ID || o.DeliveryInstructions != nil {
				t.Fatal(o)
			}
		}
		if p.Orders[0].ID <= p.Orders[1].ID {
			t.Fatal("id tie ordering is not descending")
		}
		next, err := service.ListOpenOrders(f.ctx, caller, ListRequest{Page: Page{Number: 2, Size: 2}, PickupID: pickup})
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Orders) != 1 || next.Orders[0].ID >= p.Orders[1].ID {
			t.Fatal(next)
		}
		empty, err := service.ListOpenOrders(f.ctx, caller, ListRequest{Page: Page{Number: 100, Size: 2}, PickupID: pickup})
		if err != nil || len(empty.Orders) != 0 || empty.PageInfo.Total != 3 {
			t.Fatal(empty, err)
		}
		for _, id := range openIDs {
			o, err := service.GetOrder(f.ctx, caller, strings.ToUpper(id))
			if err != nil || o.DeliveryInstructions != nil {
				t.Fatal(o, err)
			}
		}
		for _, id := range []string{own.ID, expired.ID} {
			o, err := service.GetOrder(f.ctx, caller, id)
			if id == own.ID {
				if err != nil || o.DeliveryInstructions == nil {
					t.Fatal(o, err)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		}
		// UUID user filtering uses typed equality, including uppercase valid inputs.
		p, err = service.ListUserOrders(f.ctx, Caller{ID: uuid.NewString(), Admin: true}, strings.ToUpper(requester), ListRequest{Role: RequesterRole})
		if err != nil || p.PageInfo.Total != 12 || p.PageInfo.Size != 20 {
			t.Fatal(p, err)
		}
	})
	t.Run("participant history suspended users and invisible orders", func(t *testing.T) {
		f.reset(t)
		o := sampleOrder()
		courier := uuid.NewString()
		o.CourierID = &courier
		o.Status = Completed
		f.seed(t, o)
		for _, caller := range []Caller{{ID: o.RequesterID, Suspended: true}, {ID: courier, Suspended: true}, {ID: uuid.NewString(), Admin: true}} {
			got, err := service.GetOrder(f.ctx, caller, o.ID)
			if err != nil || got.DeliveryInstructions == nil {
				t.Fatal(got, err)
			}
		}
		for _, id := range []string{o.RequesterID, courier} {
			p, err := service.ListMyOrders(f.ctx, Caller{ID: id, Suspended: true}, ListRequest{Status: Completed})
			if err != nil || len(p.Orders) != 1 || p.PageInfo.Total != 1 || p.Orders[0].DeliveryInstructions == nil {
				t.Fatal(p, err)
			}
		}
		if _, err := service.GetOrder(f.ctx, Caller{ID: uuid.NewString()}, o.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := service.GetOrder(f.ctx, Caller{ID: o.RequesterID}, uuid.NewString()); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := service.ListOpenOrders(f.ctx, Caller{ID: courier, Suspended: true}, ListRequest{}); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	})
	t.Run("offers privacy with consistent parent and relationships", func(t *testing.T) {
		f.reset(t)
		o := sampleOrder()
		o.Status = AwaitingCourier
		f.seed(t, o)
		first, second := uuid.NewString(), uuid.NewString()
		for i, courier := range []string{first, second} {
			status := OfferOpen
			if i == 1 {
				status = OfferVoid
			}
			if _, err := f.pool.Exec(f.ctx, "INSERT INTO offers VALUES($1,$2,$3,20,$4,now(),now())", uuid.NewString(), o.ID, courier, status); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range []Caller{{ID: o.RequesterID}, {ID: uuid.NewString(), Admin: true}} {
			offers, err := service.ListOffers(f.ctx, c, o.ID, "")
			if err != nil || len(offers) != 2 {
				t.Fatal(offers, err)
			}
		}
		for _, courier := range []string{first, second} {
			offers, err := service.ListOffers(f.ctx, Caller{ID: strings.ToUpper(courier)}, o.ID, "")
			if err != nil || len(offers) != 1 || offers[0].CourierID != courier {
				t.Fatal(offers, err)
			}
		}
		offers, err := service.ListOffers(f.ctx, Caller{ID: o.RequesterID}, o.ID, OfferVoid)
		if err != nil || len(offers) != 1 || offers[0].CourierID != second {
			t.Fatal(offers, err)
		}
		offers, err = service.ListOffers(f.ctx, Caller{ID: uuid.NewString()}, o.ID, "")
		if err != nil || len(offers) != 0 {
			t.Fatal(offers, err)
		}
		if _, err = f.pool.Exec(f.ctx, "UPDATE orders SET status='cancelled' WHERE id=$1", o.ID); err != nil {
			t.Fatal(err)
		}
		offers, err = service.ListOffers(f.ctx, Caller{ID: first, Suspended: true}, o.ID, "")
		if err != nil || len(offers) != 1 || offers[0].CourierID != first {
			t.Fatal(offers, err)
		}
		if _, err = service.ListOffers(f.ctx, Caller{ID: uuid.NewString()}, o.ID, ""); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	})
}
