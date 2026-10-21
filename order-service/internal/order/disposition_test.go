package order

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cancellationFixture(status Status) (*Disposition, *txFake, Order) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	courier := "courier"
	o := Order{
		ID: "37dbf670-f32c-4cca-9473-5861c274e789", RequesterID: "requester", CourierID: &courier,
		Status: status, RewardCredits: 20, ReservedCredits: 20, DeliveryDuration: 20 * time.Minute,
		Revision: 7, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		AssignedAt: &now, ExpiresAt: now.Add(time.Hour),
	}
	if status == Delivering || status == Delivered || status == Cancelled || status == Failed {
		o.PickedUpAt = &now
	}
	if status == AwaitingCourier {
		o.CourierID, o.AssignedAt = nil, nil
	}
	events := []string{}
	tx := &txFake{events: &events, order: cloneOrder(o)}
	s := NewDisposition(NewMutator(&repoFake{tx: tx}))
	s.now = func() time.Time { return now.Add(time.Minute + 123) }
	ids := 0
	s.newID = func() string { ids++; return string(rune('a' + ids)) }
	return s, tx, o
}

// Q2/Q3/Q8: terminal disposition includes timeout failures, supports corrections,
// and never repeats an outcome, warning or status event.
func TestDispositionCorrectionAndIdenticalRetry(t *testing.T) {
	for _, status := range []Status{Cancelled, Failed} {
		t.Run(string(status), func(t *testing.T) {
			s, tx, original := cancellationFixture(status)
			for i, location := range []string{"Library desk", "Residence reception"} {
				got, err := s.RecordItemLocation(context.Background(), Caller{ID: "courier", Suspended: true}, original.ID, "  "+location+"  ")
				if err != nil {
					t.Fatal(err)
				}
				if got.ItemLocation == nil || *got.ItemLocation != location || got.Revision != int64(8+i) || got.Status != status || len(tx.change.Facts) != 0 {
					t.Fatalf("result=%+v facts=%v", got, tx.change.Facts)
				}
				var audit map[string]string
				if err := json.Unmarshal(tx.change.Audit.Details, &audit); err != nil || audit["item_location"] != location {
					t.Fatalf("audit=%s error=%v", tx.change.Audit.Details, err)
				}
				tx.order = cloneOrder(got)
				applied := tx.applied
				repeated, err := s.RecordItemLocation(context.Background(), Caller{ID: "courier"}, original.ID, location)
				if err != nil || !reflect.DeepEqual(repeated, got) || tx.applied != applied {
					t.Fatalf("retry changed disposition: %+v %v", repeated, err)
				}
			}
			if original.ItemLocation != nil || tx.applied != 2 {
				t.Fatal("baseline mutated or repeat wrote")
			}
		})
	}
}

func TestDispositionChecksAuthorizationBeforeRecognizingRepeat(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller Caller
		status Status
		picked bool
		want   error
	}{
		{"requester", Caller{ID: "requester"}, Failed, true, ErrForbidden},
		{"admin", Caller{ID: "admin", Admin: true}, Failed, true, ErrNotFound},
		{"former courier", Caller{ID: "former"}, Failed, true, ErrNotFound},
		{"before pickup", Caller{ID: "courier"}, Cancelled, false, ErrPrecondition},
		{"still delivering", Caller{ID: "courier"}, Delivering, true, ErrPrecondition},
		{"anonymous", Caller{}, Failed, true, ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tx, o := cancellationFixture(tc.status)
			if !tc.picked {
				tx.order.PickedUpAt = nil
			}
			location := "Library desk"
			tx.order.ItemLocation = &location
			_, err := s.RecordItemLocation(context.Background(), tc.caller, o.ID, location)
			if !errors.Is(err, tc.want) || tx.applied != 0 {
				t.Fatalf("error=%v want=%v applied=%d", err, tc.want, tx.applied)
			}
		})
	}
}

// Existing RPC contract bounds use trimmed Unicode character counts, not bytes.
func TestCancellationAndDispositionValidateTextBeforeLocking(t *testing.T) {
	s, tx, o := cancellationFixture(Delivering)
	tooLong := strings.Repeat("界", 501)
	for _, location := range []string{"  ", tooLong, string([]byte{0xff})} {
		if _, err := s.RecordItemLocation(context.Background(), Caller{ID: "courier"}, o.ID, location); !errors.Is(err, ErrInvalid) || len(*tx.events) != 0 {
			t.Fatalf("invalid location reached lock: %v", err)
		}
	}
	if _, err := s.RecordItemLocation(context.Background(), Caller{ID: "courier"}, "invalid", "desk"); !errors.Is(err, ErrInvalid) || len(*tx.events) != 0 {
		t.Fatalf("invalid ID reached lock: %v", err)
	}
}
