//go:build integration

package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	db "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/testsupport/postgres"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/credit/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type uncertainPublisher struct {
	ids  []string
	fail bool
}

func (p *uncertainPublisher) Publish(_ context.Context, _ string, _ []byte, event proto.Message) error {
	p.ids = append(p.ids, event.(*events.AccountChanged).EventId)
	if p.fail {
		p.fail = false
		return errors.New("acknowledgement lost")
	}
	return nil
}

func TestAccountEventsCommitRollbackAndReplay(t *testing.T) {
	pool := postgres.Setup(t)
	ctx := context.Background()
	user := uuid.New()
	now := time.Now().UTC()
	store := account.NewPostgresStore(pool)
	if err := store.Open(ctx, user.String(), now); err != nil {
		t.Fatal(err)
	}
	if err := store.Open(ctx, user.String(), now); err != nil {
		t.Fatal(err)
	}
	svc := reservation.NewService(reservation.NewPostgresRepository(pool), time.Now, reservation.LatenessPayout)
	order, courier, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString()
	must := func(_ reservation.Reservation, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(svc.Reserve(ctx, order, user.String(), 20))
	must(svc.Reserve(ctx, order, user.String(), 20)) // retained replay emits nothing
	must(svc.AdjustReservation(ctx, order, attempt, courier, 30))
	must(svc.RevertAdjustment(ctx, order, attempt))
	must(svc.ReleaseReservation(ctx, order, reservation.Failed))
	order = uuid.NewString()
	must(svc.Reserve(ctx, order, user.String(), 20))
	must(svc.AdjustReservation(ctx, order, uuid.NewString(), courier, 20))
	picked := time.Now().UTC().Add(-90 * time.Minute)
	_, applied, err := svc.ApplyOutcome(
		ctx,
		reservation.OrderOutcome{
			OrderID:          order,
			Type:             reservation.OrderCompleted,
			RequesterID:      user.String(),
			CourierID:        courier,
			PickedUpAt:       picked,
			DeliveredAt:      picked.Add(90 * time.Minute),
			DeliveryDuration: time.Hour,
		},
	)
	if err != nil || !applied {
		t.Fatal(applied, err)
	}
	var n, entries, kinds int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM account_outbox),count(*),count(DISTINCT kind) FROM ledger_entries").
		Scan(&n, &entries, &kinds); err != nil ||
		n != entries ||
		kinds != 7 {
		t.Fatal("posting event coverage", n, entries, kinds, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := account.Open(ctx, db.New(tx), now, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := account.Post(ctx, db.New(tx), now, account.Posting{UserID: user, Kind: account.Reserve}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM account_outbox").Scan(&n); err != nil || n != entries {
		t.Fatal("rollback escaped", n, err)
	}
	postgres.CheckLedger(t, pool)
	sender := &uncertainPublisher{fail: true}
	relay := NewAccountPublisher(pool, sender)
	if err := relay.Drain(ctx, 1); err == nil {
		t.Fatal("uncertain publish acknowledged")
	}
	original := sender.ids[0]
	if _, err := pool.Exec(ctx, "UPDATE account_outbox SET lease_until=NULL WHERE event_id=$1", original); err != nil {
		t.Fatal(err)
	}
	if err := relay.Drain(ctx, 20); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, id := range sender.ids {
		if id == original {
			found++
		}
	}
	if found != 2 {
		t.Fatal("retry changed event identity", sender.ids)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM account_outbox WHERE published_at IS NULL").
		Scan(&n); err != nil ||
		n != 0 {
		t.Fatal("pending events", n, err)
	}
}
