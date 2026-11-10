//go:build integration

package order

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestOrderChangeMigrationPreservesOfferFacts(t *testing.T) {
	f := setup(t)
	o := f.create(t)
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	assigner := NewAssigner(f.repo, nil, AssignmentOptions{})
	courier := Caller{ID: uuid.NewString()}
	if _, err := assigner.MakeOffer(f.ctx, courier, o.ID, 20, uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := sql.Open("pgx", f.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(f.ctx, 9); err != nil {
		t.Fatal(err)
	}
	assertFacts := func(wantOffers, wantChanges int) {
		t.Helper()
		var offers, changes int
		if err := f.pool.QueryRow(f.ctx, `SELECT
			count(*) FILTER (WHERE kind='order.offer_made'),
			count(*) FILTER (WHERE kind='order.changed')
			FROM order_outbox WHERE order_id=$1`, o.ID).Scan(&offers, &changes); err != nil ||
			offers != wantOffers || changes != wantChanges {
			t.Fatalf("offer facts=%d changes=%d, want %d/%d: %v", offers, changes, wantOffers, wantChanges, err)
		}
	}
	assertFacts(1, 0)
	if _, err := provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := assigner.MakeOffer(f.ctx, courier, o.ID, 25, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	assertFacts(2, 1)
}

func TestOrderChangeAtomicityWorkflowAndPublicationLanes(t *testing.T) {
	f := setup(t)
	o := f.create(t)
	count := func() int {
		var n int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.changed'", o.ID).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("creation missing change event")
	}
	sentinel := errors.New("rollback")
	err := f.repo.Within(f.ctx, func(tx Tx) error {
		if _, err := tx.LockOrder(f.ctx, o.ID); err != nil {
			return err
		}
		tx.(*postgresTx).markChanged(o.ID, false)
		return sentinel
	})
	if !errors.Is(err, sentinel) || count() != 1 {
		t.Fatal("rollback published a change", err)
	}
	if err := f.repo.Within(f.ctx, func(Tx) error { return nil }); err != nil || count() != 1 {
		t.Fatal("no-op emitted a change", err)
	}
	// Actual pending receipt state changes do not advance the order revision.
	receipt := DeliveryReceipt{
		ID:         uuid.NewString(),
		OrderID:    o.ID,
		CourierID:  uuid.NewString(),
		ProofIDs:   []string{uuid.NewString()},
		ReceivedAt: time.Now().UTC(),
	}
	err = f.repo.WithinProofs(f.ctx, func(tx ProofTx) error {
		if _, err := tx.LockOrder(f.ctx, o.ID); err != nil {
			return err
		}
		return tx.(ReceiptTx).InsertDeliveryReceipt(f.ctx, receipt)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.repo.WithinProofs(f.ctx, func(tx ProofTx) error {
		if _, err := tx.LockOrder(f.ctx, o.ID); err != nil {
			return err
		}
		return tx.(ReceiptTx).FinishDeliveryReceipt(f.ctx, receipt.ID, "rejected", time.Now().UTC())
	})
	if err != nil || count() != 3 {
		t.Fatal("missing workflow-only changes", err, count())
	}
	var revision int64
	var records int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*),min(revision) FROM order_outbox WHERE order_id=$1 AND kind='order.changed'", o.ID).
		Scan(&records, &revision); err != nil ||
		revision != o.Revision {
		t.Fatal(records, revision, err)
	}
	// Failed UI publication must not block the subsequent business status fact.
	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE order_outbox SET published_at=clock_timestamp() WHERE kind<>'order.changed'; UPDATE order_outbox SET lease_until=clock_timestamp()+interval '1 hour' WHERE kind='order.changed'",
	); err != nil {
		t.Fatal(err)
	}
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	q := db.New(f.pool)
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	token := uuid.New()
	fact, err := q.ClaimOrderFact(f.ctx, db.ClaimOrderFactParams{LeaseToken: &token, LeaseUntil: &until, NowAt: &now})
	if err != nil || fact.Kind != "order.status_changed" {
		t.Fatal("UI event blocked business publication", fact.Kind, err)
	}
	// Offers update metadata, not status, and must still reach their owner.
	courier := uuid.NewString()
	_, err = NewAssigner(
		f.repo,
		nil,
		AssignmentOptions{},
	).MakeOffer(f.ctx, Caller{ID: courier}, o.ID, 20, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err := f.pool.QueryRow(f.ctx, "SELECT payload FROM order_outbox WHERE order_id=$1 AND kind='order.changed' ORDER BY revision DESC,ordinal DESC LIMIT 1", o.ID).
		Scan(&data); err != nil {
		t.Fatal(err)
	}
	var change ChangedPayload
	if err := json.Unmarshal(
		data,
		&change,
	); err != nil || !slices.Contains(change.Recipients, courier) ||
		!change.OpenOrders {
		t.Fatal("offer change audience", string(data), err)
	}
}
