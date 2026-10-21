//go:build integration

package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/router"
	accountrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/account"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/health"
	reservationrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/testsupport/postgres"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	ordereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// credit is a Credit service over a real database, wired as cmd/server does.
type credit struct {
	reservations creditv1connect.CreditReservationServiceClient
	accounts     creditv1connect.CreditAccountServiceClient
	outcomes     *events.Outcomes
	users        *events.Users
}

func startCredit(t *testing.T, pool *pgxpool.Pool) credit {
	t.Helper()
	clock := time.Now
	accountStore := account.NewPostgresStore(pool)
	accounts := account.NewService(accountStore, accountStore, clock)
	reservations := reservation.NewService(reservation.NewPostgresRepository(pool), clock, reservation.LatenessPayout)

	services := router.RPCServices{
		Health:      &healthrpc.Checker{DB: pool},
		Account:     &accountrpc.Server{Accounts: accounts},
		Reservation: &reservationrpc.Server{Reservations: reservations},
	}
	authenticate := httpauth.AuthenticateLocal(func(token string) (auth.Caller, error) {
		return auth.Caller{ID: token}, nil
	})

	public := httptest.NewServer(router.Public(services, authenticate))
	internal := httptest.NewServer(router.Internal(services))
	t.Cleanup(public.Close)
	t.Cleanup(internal.Close)

	return credit{
		reservations: creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, internal.URL),
		accounts:     creditv1connect.NewCreditAccountServiceClient(http.DefaultClient, public.URL),
		outcomes:     &events.Outcomes{Settler: reservations, Backoff: 10 * time.Millisecond},
		users:        &events.Users{Opener: accounts},
	}
}

func (c credit) account(t *testing.T, userID string) *creditv1.Account {
	t.Helper()
	res, err := c.accounts.GetMyAccount(context.Background(), withBearer(&creditv1.GetMyAccountRequest{}, userID))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetAccount()
}

func (c credit) ledger(t *testing.T, userID string) []creditv1.LedgerEntryKind {
	t.Helper()
	res, err := c.accounts.ListMyLedgerEntries(context.Background(), withBearer(&creditv1.ListMyLedgerEntriesRequest{}, userID))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]creditv1.LedgerEntryKind, len(res.Msg.GetEntries()))
	for i, e := range res.Msg.GetEntries() {
		kinds[i] = e.GetKind()
	}
	return kinds
}

func TestCreditOverPostgres(t *testing.T) {
	pool := postgres.Setup(t)
	c := startCredit(t, pool)
	ctx := context.Background()
	t.Cleanup(func() { postgres.CheckLedger(t, pool) })

	t.Run("embedded migrations match the schema directory", func(t *testing.T) {
		var databaseURL = pool.Config().ConnString()
		if err := database.Migrate(ctx, databaseURL); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an order outcome settles a reservation made over RPC once", func(t *testing.T) {
		order, requester, courier, offer := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

		_, err := c.reservations.Reserve(ctx, connect.NewRequest(&creditv1.ReserveRequest{RequestId: order, RequesterId: requester, Amount: 30}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.reservations.AdjustReservation(ctx, connect.NewRequest(&creditv1.AdjustReservationRequest{
			RequestId:   order,
			OfferId:     offer,
			CourierId:   courier,
			OfferAmount: 30,
		}))
		if err != nil {
			t.Fatal(err)
		}

		// Ninety minutes against an hour allowed pays half the offer.
		picked := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
		event := &ordereventsv1.OrderOutcome{
			EventId:          uuid.NewString(),
			OrderId:          order,
			Outcome:          ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED,
			RequesterId:      requester,
			CourierId:        proto.String(courier),
			ReservedCredits:  30,
			DeliveryDuration: durationpb.New(time.Hour),
			PickedUpAt:       timestamppb.New(picked),
			DeliveredAt:      timestamppb.New(picked.Add(90 * time.Minute)),
		}
		for range 2 {
			if err := c.outcomes.Handle(ctx, kafka.Metadata{Topic: events.OrderOutcomeTopic}, event); err != nil {
				t.Fatal(err)
			}
		}

		res, err := c.reservations.GetReservation(ctx, connect.NewRequest(&creditv1.GetReservationRequest{RequestId: order}))
		if err != nil {
			t.Fatal(err)
		}
		if s := res.Msg.GetReservation().GetSettlement(); s.GetPaidToCourier() != 15 || s.GetReturnedToRequester() != 15 {
			t.Fatalf("settlement: %v", s)
		}
		if a := c.account(t, requester); a.GetBalance() != 85 || a.GetReserved() != 0 {
			t.Fatalf("requester: %v", a)
		}
		if a := c.account(t, courier); a.GetBalance() != 115 {
			t.Fatalf("courier: %v", a)
		}

		// Newest first: one payout and one refund despite the redelivery.
		want := []creditv1.LedgerEntryKind{
			creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_REFUND,
			creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_PAYOUT,
			creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_RESERVE,
			creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_GRANT,
		}
		if got := c.ledger(t, requester); !equalKinds(got, want) {
			t.Fatalf("requester ledger %v", got)
		}
	})

	t.Run("a mismatched outcome is acknowledged without moving credits", func(t *testing.T) {
		order, requester := uuid.NewString(), uuid.NewString()
		_, err := c.reservations.Reserve(ctx, connect.NewRequest(&creditv1.ReserveRequest{RequestId: order, RequesterId: requester, Amount: 10}))
		if err != nil {
			t.Fatal(err)
		}

		// A payout for a reservation no courier was assigned to.
		event := &ordereventsv1.OrderOutcome{
			EventId:     uuid.NewString(),
			OrderId:     order,
			Outcome:     ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP,
			RequesterId: requester,
			CourierId:   proto.String(uuid.NewString()),
		}
		if err := c.outcomes.Handle(ctx, kafka.Metadata{Topic: events.OrderOutcomeTopic}, event); err != nil {
			t.Fatal(err)
		}
		if a := c.account(t, requester); a.GetBalance() != 100 || a.GetReserved() != 10 {
			t.Fatalf("requester: %v", a)
		}
	})

	t.Run("a new user is granted the starting credits once", func(t *testing.T) {
		user := uuid.NewString()
		_, err := c.accounts.GetMyAccount(ctx, withBearer(&creditv1.GetMyAccountRequest{}, user))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("before users.create: %v", err)
		}

		event := &userseventsv1.UserCreated{EventId: uuid.NewString(), UserId: user}
		for range 2 {
			if err := c.users.Handle(ctx, kafka.Metadata{Topic: events.UserCreatedTopic}, event); err != nil {
				t.Fatal(err)
			}
		}

		if a := c.account(t, user); a.GetBalance() != account.StartingCredits {
			t.Fatalf("account: %v", a)
		}
		if got := c.ledger(t, user); !equalKinds(got, []creditv1.LedgerEntryKind{creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_GRANT}) {
			t.Fatalf("ledger %v", got)
		}
	})
}

func equalKinds(a, b []creditv1.LedgerEntryKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
