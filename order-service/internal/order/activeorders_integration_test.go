//go:build integration

package order

import (
	"testing"

	"github.com/google/uuid"
)

// ListMyOrders(active_only) backs the frontend's Ongoing list: orders the caller
// can still act on, excluding ended orders and orders they withdrew from.
func TestActiveOnlyListsCurrentParticipation(t *testing.T) {
	f := setup(t)
	s := NewService(f.repo)

	requester := Caller{ID: uuid.NewString()}
	for _, status := range []Status{
		PendingReservation, AwaitingCourier, AwaitingPickup, Delivering, Delivered,
		Completed, Cancelled, Expired, Failed, Rejected,
	} {
		o := sampleOrder()
		o.RequesterID = requester.ID
		o.Status = status
		if status == AwaitingPickup || status == Delivering || status == Delivered || status == Completed {
			courier := uuid.NewString()
			o.CourierID = &courier
		}
		f.seed(t, o)
	}

	// The requester sees only the five orders that haven't ended.
	page, err := s.ListMyOrders(f.ctx, requester, ListRequest{ActiveOnly: true})
	if err != nil || page.PageInfo.Total != 5 || len(page.Orders) != 5 {
		t.Fatalf("active orders: %+v %v", page.PageInfo, err)
	}
	for _, o := range page.Orders {
		if o.Status.IsTerminal() {
			t.Fatalf("terminal order listed: %s", o.Status)
		}
	}
	page, err = s.ListMyOrders(f.ctx, requester, ListRequest{ActiveOnly: true, Status: Delivering})
	if err != nil || page.PageInfo.Total != 1 {
		t.Fatalf("active and status: %+v %v", page.PageInfo, err)
	}
	page, err = s.ListMyOrders(f.ctx, requester, ListRequest{})
	if err != nil || page.PageInfo.Total != 10 {
		t.Fatalf("unfiltered: %+v %v", page.PageInfo, err)
	}

	// A courier who withdrew keeps the order in their history but not in active results.
	o := f.create(t)
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	withdrawn, current := Actor{Kind: User, ID: uuid.NewString()}, Actor{Kind: User, ID: uuid.NewString()}
	f.mustTransition(t, o.ID, Assign, withdrawn)
	f.mustTransition(t, o.ID, Withdraw, withdrawn)
	f.mustTransition(t, o.ID, Assign, current)

	page, err = s.ListMyOrders(f.ctx, Caller{ID: withdrawn.ID}, ListRequest{ActiveOnly: true})
	if err != nil || page.PageInfo.Total != 0 {
		t.Fatalf("withdrawn courier active: %+v %v", page.PageInfo, err)
	}
	page, err = s.ListMyOrders(f.ctx, Caller{ID: withdrawn.ID}, ListRequest{})
	if err != nil || page.PageInfo.Total != 1 {
		t.Fatalf("withdrawn courier history: %+v %v", page.PageInfo, err)
	}
	page, err = s.ListMyOrders(f.ctx, Caller{ID: current.ID}, ListRequest{ActiveOnly: true, Role: CourierRole})
	if err != nil || page.PageInfo.Total != 1 {
		t.Fatalf("current courier active: %+v %v", page.PageInfo, err)
	}
}
