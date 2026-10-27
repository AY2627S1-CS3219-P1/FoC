package chat

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	"github.com/google/uuid"
)

// newPostgresStore needs TEST_DATABASE_URL pointing at a throwaway database,
// because it wipes the chat tables.
func newPostgresStore(t *testing.T) *PostgresStore {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE order_participants, chat_messages, chat_reply_targets, chat_selections, order_notifications_sent`); err != nil {
		t.Fatal(err)
	}
	return &PostgresStore{Pool: pool}
}

func TestPostgresOrdersIgnoreOlderRevisions(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	o := order(AwaitingPickup)
	o.Revision = 5
	if err := s.SaveOrder(ctx, o, now); err != nil {
		t.Fatal(err)
	}

	// An admin unassigned the courier at revision 6; a replay of 5 arrives late.
	unassigned := o
	unassigned.Revision, unassigned.CourierID, unassigned.Status = 6, uuid.Nil, "awaiting_courier"
	for _, change := range []Order{unassigned, o} {
		if err := s.SaveOrder(ctx, change, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetOrder(ctx, o.ID)
	if err != nil || got.Revision != 6 || got.CourierID != uuid.Nil || got.IsOpen(now) {
		t.Fatalf("stored = %+v, %v", got, err)
	}
	if _, err := s.GetOrder(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order: %v", err)
	}
}

func TestPostgresUserOrdersAndRouting(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	active, recent, old, other := order(Delivering), order(Completed), order(Cancelled), order(Delivering)
	recent.EndedAt = now.Add(-time.Minute)
	old.EndedAt = now.Add(-2 * GracePeriod)
	other.RequesterID, other.CourierID = uuid.New(), uuid.New()
	for _, o := range []Order{active, recent, old, other} {
		if err := s.SaveOrder(ctx, o, now); err != nil {
			t.Fatal(err)
		}
	}
	orders, err := s.UserOrders(ctx, courier, now.Add(-GracePeriod))
	if err != nil || len(orders) != 2 {
		t.Fatalf("user orders = %+v, %v", orders, err)
	}

	if err := s.SaveReplyTarget(ctx, requesterChat, 55, active.ID, now); err != nil {
		t.Fatal(err)
	}
	if id, err := s.ReplyTarget(ctx, requesterChat, 55); err != nil || id != active.ID {
		t.Fatalf("reply target = %v, %v", id, err)
	}
	if _, err := s.ReplyTarget(ctx, requesterChat, 56); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown reply target: %v", err)
	}
	for _, id := range []uuid.UUID{recent.ID, active.ID} {
		if err := s.Select(ctx, requester, id, now); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := s.Selection(ctx, requester); err != nil || id != active.ID {
		t.Fatalf("selection = %v, %v", id, err)
	}
}

func TestPostgresDeleteExpired(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	expired, closed, active := order(Completed), order(Cancelled), order(Delivering)
	expired.EndedAt = now.Add(-Retention - time.Hour)
	closed.EndedAt = now.Add(-2 * GracePeriod)
	for i, o := range []Order{expired, closed, active} {
		if err := s.SaveOrder(ctx, o, now); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveMessage(ctx, Message{ID: uuid.New(), OrderID: o.ID, SenderID: courier, RecipientID: requester, Body: "hi", SentAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveReplyTarget(ctx, requesterChat, int64(i+1), o.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Select(ctx, requester, closed.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(ctx, courier, active.ID, now); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteExpired(ctx, now.Add(-Retention), now.Add(-GracePeriod))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted = %d, %v", deleted, err)
	}
	count := func(query string, args ...any) int {
		var n int
		if err := s.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM chat_messages WHERE order_id = $1`, expired.ID); n != 0 {
		t.Fatalf("expired messages left: %d", n)
	}
	if n := count(`SELECT count(*) FROM chat_messages`); n != 2 {
		t.Fatalf("messages left: %d", n)
	}
	if n := count(`SELECT count(*) FROM order_participants WHERE order_id = $1`, expired.ID); n != 0 {
		t.Fatalf("expired order left: %d", n)
	}
	if n := count(`SELECT count(*) FROM chat_reply_targets WHERE order_id = $1`, expired.ID); n != 0 {
		t.Fatalf("expired reply targets left: %d", n)
	}
	if _, err := s.Selection(ctx, requester); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closed selection kept: %v", err)
	}
	if id, err := s.Selection(ctx, courier); err != nil || id != active.ID {
		t.Fatalf("active selection = %v, %v", id, err)
	}
}

func TestPostgresUserOrdersLimitsEligibleOrders(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	active := order(Delivering)
	if err := s.SaveOrder(ctx, active, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// More than a keyboard's worth of newer, ineligible orders must not
	// displace the active order before the relay checks chat eligibility.
	for i := range 25 {
		ineligible := order("awaiting_courier")
		if i%2 == 0 {
			ineligible.CourierID = uuid.Nil
		} else {
			ineligible.Status = Completed
			// A terminal order without an end timestamp is also ineligible.
		}
		if err := s.SaveOrder(ctx, ineligible, now); err != nil {
			t.Fatal(err)
		}
	}

	orders, err := s.UserOrders(ctx, requester, now.Add(-GracePeriod))
	if err != nil || len(orders) != 1 || orders[0].ID != active.ID {
		t.Fatalf("eligible orders = %+v, %v", orders, err)
	}
}

func TestPostgresRetentionPreservesEligibleReplyTargets(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	old := now.Add(-Retention - time.Hour)
	cutoff := now.Add(-Retention)
	var nextMessageID int64
	for _, tc := range []struct {
		name   string
		order  Order
		retain bool
	}{
		{name: "awaiting pickup", order: order(AwaitingPickup), retain: true},
		{name: "delivering", order: order(Delivering), retain: true},
		{name: "delivered", order: order(Delivered), retain: true},
		{name: "recent completed", order: order(Completed), retain: true},
		{name: "recent cancelled", order: order(Cancelled), retain: true},
		{name: "recent failed", order: order(Failed), retain: true},
		{name: "expired completed", order: order(Completed)},
		{name: "expired cancelled", order: order(Cancelled)},
		{name: "expired failed", order: order(Failed)},
		{name: "unassigned", order: order(AwaitingPickup)},
		{name: "non-chat status", order: order("awaiting_courier")},
		{name: "terminal without end", order: order(Completed)},
		{name: "missing projection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.order
			switch tc.name {
			case "recent completed", "recent cancelled", "recent failed":
				o.EndedAt = now.Add(-time.Hour)
			case "expired completed", "expired cancelled", "expired failed":
				o.EndedAt = old
			case "unassigned":
				o.CourierID = uuid.Nil
			}
			if o.ID != uuid.Nil {
				if err := s.SaveOrder(ctx, o, now); err != nil {
					t.Fatal(err)
				}
			} else {
				o.ID = uuid.New()
			}
			nextMessageID++
			if err := s.SaveReplyTarget(ctx, requesterChat, nextMessageID, o.ID, old); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteExpired(ctx, cutoff, now.Add(-GracePeriod)); err != nil {
				t.Fatal(err)
			}
			_, err := s.ReplyTarget(ctx, requesterChat, nextMessageID)
			if tc.retain && err != nil {
				t.Fatalf("retained reply target: %v", err)
			}
			if !tc.retain && !errors.Is(err, ErrNotFound) {
				t.Fatalf("expired reply target: %v", err)
			}

			// New ineligible or orphan targets have a full creation-time
			// retention period before cleanup can remove them.
			if !tc.retain && o.EndedAt.IsZero() {
				nextMessageID++
				if err := s.SaveReplyTarget(ctx, requesterChat, nextMessageID, o.ID, cutoff); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DeleteExpired(ctx, cutoff, now.Add(-GracePeriod)); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReplyTarget(ctx, requesterChat, nextMessageID); err != nil {
					t.Fatalf("target at retention boundary: %v", err)
				}
			}
		})
	}
}

func TestPostgresRetentionRemovesOldOrphanMessages(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	cutoff := now.Add(-Retention)
	active, recent := order(Delivering), order(Completed)
	recent.EndedAt = now.Add(-time.Hour)
	for _, o := range []Order{active, recent} {
		if err := s.SaveOrder(ctx, o, now); err != nil {
			t.Fatal(err)
		}
	}

	var retained []uuid.UUID
	for _, tc := range []struct {
		orderID uuid.UUID
		sentAt  time.Time
	}{
		{uuid.New(), cutoff.Add(-time.Hour)},
		{uuid.New(), cutoff},
		{uuid.New(), now},
		{active.ID, cutoff.Add(-time.Hour)},
		{recent.ID, cutoff.Add(-time.Hour)},
	} {
		id := uuid.New()
		if err := s.SaveMessage(ctx, Message{ID: id, OrderID: tc.orderID, SenderID: courier, RecipientID: requester, Body: "hi", SentAt: tc.sentAt}); err != nil {
			t.Fatal(err)
		}
		if tc.sentAt.Before(cutoff) && tc.orderID != active.ID && tc.orderID != recent.ID {
			continue
		}
		retained = append(retained, id)
	}

	deleted, err := s.DeleteExpired(ctx, cutoff, now.Add(-GracePeriod))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted = %d, %v", deleted, err)
	}
	for _, id := range retained {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_messages WHERE id = $1)`, id).Scan(&exists); err != nil || !exists {
			t.Fatalf("retained message %v: exists=%v, err=%v", id, exists, err)
		}
	}
}

func TestPostgresRetentionRemovesOrphanAndClosedSelections(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	if err := s.Select(ctx, requester, uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	closed := order(Completed)
	closed.EndedAt = now.Add(-GracePeriod)
	if err := s.SaveOrder(ctx, closed, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(ctx, courier, closed.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteExpired(ctx, now.Add(-Retention), now.Add(-GracePeriod)); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{requester, courier} {
		if _, err := s.Selection(ctx, userID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("orphan or closed selection kept for %v: %v", userID, err)
		}
	}
}
