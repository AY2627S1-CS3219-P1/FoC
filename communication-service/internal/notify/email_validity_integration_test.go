package notify

import (
	"context"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func assertEmailSkipped(t *testing.T, pool *pgxpool.Pool, eventID, userID uuid.UUID, reason string) {
	t.Helper()
	var status, detail string
	var attempts int
	if err := pool.QueryRow(context.Background(), `SELECT status, last_error, attempts FROM notification_emails
		WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&status, &detail, &attempts); err != nil || status != "skipped" || detail != reason || attempts != 0 {
		t.Fatalf("status=%q reason=%q attempts=%d err=%v", status, detail, attempts, err)
	}
	if accepted, err := (&PostgresDeliveries{Queries: db.New(pool)}).HasBeenAccepted(
		context.Background(),
		eventID,
		userID,
	); err != nil ||
		!accepted {
		t.Fatalf("skipped delivery must remain accepted: %v, %v", accepted, err)
	}
}

func TestQueuedOfferExpiresWhileWaitingForAnEmail(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx := context.Background()
	queue := &PostgresEmailQueue{Pool: pool}
	handler, _, _, _ := newHandler()
	handler.Sender = nil
	handler.Deliveries = &PostgresDeliveries{Queries: db.New(pool)}
	handler.EmailQueue = queue
	event := offerEvent(time.Minute)
	if err := handler.HandleOffer(ctx, kafka.Metadata{}, event); err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	worker := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
	if processed, err := worker.DeliverNext(ctx); err != nil || processed {
		t.Fatalf("unexpired offer must wait for the email: processed=%v err=%v", processed, err)
	}
	worker.Now = func() time.Time { return now.Add(LateWindow) }
	if processed, err := worker.DeliverNext(ctx); err != nil || !processed || len(mailer.sent) != 0 {
		t.Fatalf("expired offer must be skipped: processed=%v emails=%+v err=%v", processed, mailer.sent, err)
	}
	assertEmailSkipped(t, pool, eventID, requesterID, "notification expired")
	projectEmail(t, pool, requesterID, "alice@foc.local")
	if err := handler.HandleOffer(ctx, kafka.Metadata{}, event); err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.DeliverNext(ctx); err != nil || processed || len(mailer.sent) != 0 {
		t.Fatalf("late projection or replay revived an expired offer: processed=%v emails=%+v err=%v",
			processed, mailer.sent, err)
	}
}

func TestQueuedAcceptanceRechecksOrderRevision(t *testing.T) {
	for _, state := range []string{"current", "superseded", "removed", "not yet stored"} {
		t.Run(state, func(t *testing.T) {
			pool := openEmailPostgres(t)
			ctx := context.Background()
			queue := &PostgresEmailQueue{Pool: pool}
			handler, _, _, _ := newHandler()
			handler.Sender = nil
			handler.Deliveries = &PostgresDeliveries{Queries: db.New(pool)}
			handler.EmailQueue = queue
			event := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, user(requesterID))
			handler.Orders = &fakeOrders{revisions: map[string]int64{event.OrderId: event.Revision}}
			if _, err := pool.Exec(ctx, `INSERT INTO order_participants
				(order_id, requester_id, courier_id, status, revision, pickup_location_name, updated_at)
				VALUES ($1, $2, $3, 'awaiting_pickup', $4, 'Fine Food', $5)`,
				event.OrderId, requesterID, courierID, event.Revision, now); err != nil {
				t.Fatal(err)
			}
			if err := handler.Handle(ctx, kafka.Metadata{}, event); err != nil {
				t.Fatal(err)
			}
			if state == "superseded" {
				if _, err := pool.Exec(ctx, `UPDATE order_participants SET revision = revision + 1`); err != nil {
					t.Fatal(err)
				}
			}
			if state == "removed" || state == "not yet stored" {
				if _, err := pool.Exec(ctx, `DELETE FROM order_participants`); err != nil {
					t.Fatal(err)
				}
			}
			mailer := &fakeMailer{}
			worker := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
			// A missing order is a deleted one only once the email is past the
			// late window; while fresh, the participants may not have stored it.
			if state == "removed" {
				worker.Now = func() time.Time { return now.Add(LateWindow + time.Minute) }
			}
			wantSent := state == "current" || state == "not yet stored"
			if !wantSent {
				if processed, err := worker.DeliverNext(ctx); err != nil || !processed || len(mailer.sent) != 0 {
					t.Fatalf(
						"obsolete acceptance must be skipped without waiting for email: processed=%v emails=%+v err=%v",
						processed,
						mailer.sent,
						err,
					)
				}
				assertEmailSkipped(t, pool, eventID, courierID, "order superseded")
			}
			projectEmail(t, pool, courierID, "ben@foc.local")
			processed, err := worker.DeliverNext(ctx)
			if err != nil || processed != wantSent || (len(mailer.sent) == 1) != wantSent {
				t.Fatalf("state=%s processed=%v emails=%+v err=%v", state, processed, mailer.sent, err)
			}
		})
	}
}

func TestQueuedUnassignmentRechecksCourier(t *testing.T) {
	for _, state := range []string{"returning courier", "different courier", "unassigned", "removed order"} {
		t.Run(state, func(t *testing.T) {
			pool := openEmailPostgres(t)
			ctx := context.Background()
			queue := &PostgresEmailQueue{Pool: pool}
			handler, _, _, _ := newHandler()
			handler.Sender = nil
			handler.Deliveries = &PostgresDeliveries{Queries: db.New(pool)}
			handler.EmailQueue = queue
			event := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				user(requesterID),
			)
			event.CourierId = nil
			event.PreviousCourierId = proto.String(courierID.String())
			if _, err := pool.Exec(ctx, `INSERT INTO order_participants
    (order_id, requester_id, status, revision, pickup_location_name, updated_at)
    VALUES ($1, $2, 'awaiting_courier', $3, 'Fine Food', $4)`, event.OrderId, requesterID, event.Revision, now); err != nil {
				t.Fatal(err)
			}
			if err := handler.Handle(ctx, kafka.Metadata{}, event); err != nil {
				t.Fatal(err)
			}
			// Missing email keeps the notice pending while the assignment changes.
			mailer := &fakeMailer{}
			worker := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
			if processed, err := worker.DeliverNext(ctx); err != nil || processed {
				t.Fatalf("must wait for address: %v %v", processed, err)
			}
			if state == "returning courier" || state == "different courier" {
				current := courierID
				if state == "different courier" {
					current = uuid.New()
				}
				if _, err := pool.Exec(
					ctx,
					`UPDATE order_participants SET courier_id=$1, revision=revision+1`,
					current,
				); err != nil {
					t.Fatal(err)
				}
			}
			if state == "removed order" {
				if _, err := pool.Exec(ctx, `DELETE FROM order_participants`); err != nil {
					t.Fatal(err)
				}
			}
			if state == "returning courier" {
				if processed, err := worker.DeliverNext(ctx); err != nil || !processed {
					t.Fatalf("must skip stale notice even without address: %v %v", processed, err)
				}
				assertEmailSkipped(t, pool, eventID, courierID, "courier reassigned")
			}
			projectEmail(t, pool, courierID, "ben@foc.local")
			processed, err := worker.DeliverNext(ctx)
			wantSent := 1
			if state == "returning courier" {
				wantSent = 0
			}
			if err != nil || processed != (wantSent == 1) || len(mailer.sent) != wantSent {
				t.Fatalf("processed=%v sent=%v err=%v", processed, mailer.sent, err)
			}
		})
	}
}
