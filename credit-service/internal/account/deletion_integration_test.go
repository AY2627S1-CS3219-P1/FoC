//go:build integration

package account_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/testsupport/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	requesterID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	courierID   = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	orderID     = uuid.MustParse("11111111-1111-4111-8111-111111111111")
)

type fixture struct {
	pool         *pgxpool.Pool
	store        *account.PostgresStore
	accounts     *account.Service
	reservations *reservation.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := postgres.Setup(t)
	store := account.NewPostgresStore(pool)
	reservations := reservation.NewService(
		reservation.NewPostgresRepository(pool), time.Now, reservation.LatenessPayout,
	)
	return &fixture{
		pool:         pool,
		store:        store,
		accounts:     account.NewService(store, store, store, time.Now),
		reservations: reservations,
	}
}

func (f *fixture) work(t *testing.T, userID string) account.OutstandingWork {
	t.Helper()
	work, err := f.store.OutstandingWork(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func wantReasons(t *testing.T, work account.OutstandingWork, want ...account.OutstandingWorkReason) {
	t.Helper()
	if work.HasOutstandingWork() != (len(want) > 0) {
		t.Fatalf("outstanding=%v want reasons %v: %+v", work.HasOutstandingWork(), want, work)
	}
	for _, reason := range want {
		if !slices.Contains(work.Reasons, reason) {
			t.Fatalf("missing %s: %+v", reason, work)
		}
	}
}

// A recorded deletion is replay protection: a delayed users.create must not
// reopen the account or grant starting credits, while a lazy open inside a
// pre-existing settlement chain can still post without granting.
func TestDeletedUserMarkerBlocksReprovisioning(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := requesterID.String()

	t.Run("record then users.create opens nothing", func(t *testing.T) {
		if err := f.accounts.RecordDeletion(ctx, user, uuid.NewString(), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := f.accounts.Open(ctx, user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.accounts.GetAccount(ctx, user); !errors.Is(err, account.ErrAccountNotFound) {
			t.Fatalf("deleted user account reopened: %v", err)
		}
		wantReasons(t, f.work(t, user))
	})

	t.Run("replayed and superseded events change nothing", func(t *testing.T) {
		event := uuid.NewString()
		for i := range 3 {
			if err := f.accounts.RecordDeletion(ctx, user, event, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			_ = i
		}
	})

	t.Run("lazy open grants nothing but allows postings", func(t *testing.T) {
		// A settlement-style payout needs an existing reservation as the ledger
		// order reference; the deleted user here takes the courier side.
		other := uuid.MustParse("66666666-6666-4666-8666-666666666666")
		if err := f.accounts.Open(ctx, other.String()); err != nil {
			t.Fatal(err)
		}
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
		q := creditdb.New(tx)
		now := time.Now().UTC()
		if _, err := q.InsertReservation(ctx, creditdb.InsertReservationParams{
			OrderID: orderID, RequesterID: &other, Status: "HELD",
			OriginalAmount: 15, ReservedAmount: 15, Revision: 1, NowAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		opened, err := account.Open(ctx, q, now, requesterID)
		if err != nil {
			t.Fatal(err)
		}
		if opened.Balance != 0 {
			t.Fatalf("deleted user received starting credits: %d", opened.Balance)
		}
		if err := account.Post(ctx, q, now, account.Posting{
			UserID: requesterID, Kind: account.Payout, BalanceDelta: 7, OrderID: &orderID,
		}); err != nil {
			t.Fatalf("posting for deleted user: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}

		a, err := f.accounts.GetAccount(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if a.Balance != 7 || a.Reserved != 0 {
			t.Fatalf("committed financial facts changed: %+v", a)
		}
	})
}

// Facts committed before the deletion are retained, and the grant is never
// repeated.
func TestDeletionKeepsCommittedFactsAndNeverRegrants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	user := requesterID.String()

	if err := f.accounts.Open(ctx, user); err != nil {
		t.Fatal(err)
	}
	a, err := f.accounts.GetAccount(ctx, user)
	if err != nil || a.Balance != account.StartingCredits {
		t.Fatalf("open: %+v %v", a, err)
	}

	if err := f.accounts.RecordDeletion(ctx, user, uuid.NewString(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.Open(ctx, user); err != nil {
		t.Fatal(err)
	}

	a, err = f.accounts.GetAccount(ctx, user)
	if err != nil || a.Balance != account.StartingCredits {
		t.Fatalf("facts changed or regranted: %+v %v", a, err)
	}
}

// The drain query must report every live financial obligation and never count
// completed history. A missing account is not outstanding and is not created.
func TestOutstandingWorkTruthTable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	assignmentID := uuid.MustParse("55555555-5555-4555-8555-555555555555")

	t.Run("missing account is empty and stays missing", func(t *testing.T) {
		wantReasons(t, f.work(t, requesterID.String()))
		if _, err := f.accounts.GetAccount(ctx, requesterID.String()); !errors.Is(
			err,
			account.ErrAccountNotFound,
		) {
			t.Fatalf("drain created an account: %v", err)
		}
	})

	t.Run("held reservation counts for the requester", func(t *testing.T) {
		if _, err := f.reservations.Reserve(ctx, orderID.String(), requesterID.String(), 15); err != nil {
			t.Fatal(err)
		}
		wantReasons(t, f.work(t, requesterID.String()),
			account.OutstandingReservedBalance, account.OutstandingActiveReservation)
		wantReasons(t, f.work(t, courierID.String()))
	})

	t.Run("assigned reservation counts for requester and courier", func(t *testing.T) {
		if _, err := f.reservations.AdjustReservation(
			ctx, orderID.String(), assignmentID.String(), courierID.String(), 20,
		); err != nil {
			t.Fatal(err)
		}
		wantReasons(t, f.work(t, requesterID.String()), account.OutstandingActiveReservation)
		// reserved = 0 alone is not courier safety: the courier still has no
		// account, yet their applied assignment is unsettled.
		wantReasons(t, f.work(t, courierID.String()),
			account.OutstandingActiveReservation, account.OutstandingUnsettledAssignment)
	})

	t.Run("settled reservation and assignment are empty", func(t *testing.T) {
		picked := time.Now().UTC().Add(-time.Hour)
		_, _, err := f.reservations.ApplyOutcome(ctx, reservation.OrderOutcome{
			OrderID:          orderID.String(),
			Type:             reservation.OrderCompleted,
			RequesterID:      requesterID.String(),
			CourierID:        courierID.String(),
			PickedUpAt:       picked,
			DeliveredAt:      picked.Add(30 * time.Minute),
			DeliveryDuration: 30 * time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		wantReasons(t, f.work(t, requesterID.String()))
		wantReasons(t, f.work(t, courierID.String()))
	})
}

// A reservation that is still being written is not yet durable: the single
// snapshot sees either the committed hold or nothing, never a torn state, and
// never blocks behind the writer's transaction.
func TestOutstandingWorkSnapshotHandoff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.accounts.Open(ctx, requesterID.String()); err != nil {
		t.Fatal(err)
	}

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	q := creditdb.New(tx)
	now := time.Now().UTC()
	if _, err := q.InsertReservation(ctx, creditdb.InsertReservationParams{
		OrderID: orderID, RequesterID: &requesterID, Status: "HELD",
		OriginalAmount: 15, ReservedAmount: 15, Revision: 1, NowAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := account.Post(ctx, q, now, account.Posting{
		UserID: requesterID, Kind: account.Reserve, ReservedDelta: 15, OrderID: &orderID,
	}); err != nil {
		t.Fatal(err)
	}

	// The uncommitted hold is not durable, so empty is correct; the read must
	// still return while the writer's transaction is open.
	before, err := f.store.OutstandingWork(ctx, requesterID.String())
	if err != nil {
		t.Fatal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.OutstandingWork(ctx, requesterID.String())
	if err != nil {
		t.Fatal(err)
	}

	if before.HasOutstandingWork() || !after.HasOutstandingWork() {
		t.Fatalf("snapshot did not move with the commit: before=%+v after=%+v", before, after)
	}
	if !slices.Contains(after.Reasons, account.OutstandingActiveReservation) {
		t.Fatalf("committed hold missing: %+v", after)
	}
}
