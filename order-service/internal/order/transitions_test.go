package order

import (
	"errors"
	"testing"
)

func TestTransitions(t *testing.T) {
	requester, courier, stranger, admin, system := Actor{User, "requester"}, Actor{User, "courier"}, Actor{User, "other"}, Actor{Admin, "admin"}, Actor{Kind: System}
	courierID := "courier"
	cases := []struct {
		from   Status
		action Action
		actor  Actor
		to     Status
	}{
		{PendingReservation, ConfirmReservation, system, AwaitingCourier}, {PendingReservation, RefuseReservation, system, Rejected},
		{AwaitingCourier, Assign, stranger, AwaitingPickup}, {AwaitingCourier, Cancel, requester, Cancelled}, {AwaitingCourier, Cancel, admin, Cancelled}, {AwaitingCourier, Expire, system, Expired},
		{AwaitingPickup, Pickup, courier, Delivering}, {AwaitingPickup, Cancel, requester, Cancelled}, {AwaitingPickup, Cancel, admin, Cancelled}, {AwaitingPickup, Withdraw, courier, AwaitingCourier}, {AwaitingPickup, Unassign, admin, AwaitingCourier},
		{Delivering, Deliver, courier, Delivered}, {Delivering, Cancel, requester, Cancelled}, {Delivering, Cancel, courier, Failed}, {Delivering, Timeout, system, Failed}, {Delivered, ConfirmReceipt, requester, Completed},
	}
	statuses := []Status{PendingReservation, AwaitingCourier, AwaitingPickup, Delivering, Delivered, Completed, Cancelled, Expired, Failed, Rejected, "invalid"}
	actions := []Action{ConfirmReservation, RefuseReservation, Assign, Cancel, Expire, Pickup, Withdraw, Unassign, Deliver, Timeout, ConfirmReceipt, "invalid"}
	actors := []Actor{requester, courier, stranger, admin, system, {User, ""}, {ActorKind("bad"), "other"}}
	for _, s := range statuses {
		for _, a := range actions {
			for _, actor := range actors {
				o := Order{RequesterID: "requester", CourierID: &courierID, Status: s}
				next, repeat, err := Transition(o, a, actor)
				expected := Status("")
				for _, c := range cases {
					if c.from == s && c.action == a && c.actor == actor {
						expected = c.to
					}
				}
				// Assignment accepts any eligible non-requester; an existing relationship
				// on Awaiting Courier is not normally persisted, but is still non-self.
				if s == AwaitingCourier && a == Assign && actor == courier {
					expected = AwaitingPickup
				}
				expectedRepeat := (s == AwaitingPickup && a == Assign && actor == courier) || (s == Completed && a == ConfirmReceipt && actor == requester)
				if expectedRepeat {
					expected = s
				}
				if expected != "" {
					if err != nil || next != expected || repeat != expectedRepeat {
						t.Errorf("%s %s %+v: %s %v %v", s, a, actor, next, repeat, err)
					}
				} else if err == nil {
					t.Errorf("unexpected legal transition: %s %s %+v", s, a, actor)
				}
			}
		}
	}
	if _, _, err := Transition(Order{RequesterID: "requester", Status: AwaitingCourier}, Assign, requester); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}
func TestCancellationDefaultsAndRepeats(t *testing.T) {
	for _, s := range []Status{PendingReservation, Delivered, Completed, Cancelled, Rejected} {
		for _, a := range []Actor{{User, "requester"}, {Admin, "admin"}, {Kind: System}} {
			if _, repeat, err := Transition(Order{RequesterID: "requester", Status: s}, Cancel, a); err == nil || repeat {
				t.Fatal(s, a)
			}
		}
	}
	// A matching destination alone is insufficient to recognize a repeat.
	if _, repeat, err := Transition(Order{RequesterID: "requester", Status: Delivering}, Pickup, Actor{User, "requester"}); err == nil || repeat {
		t.Fatal("pickup repeat inferred from status")
	}
}
