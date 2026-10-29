package main

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/router"
	accountrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/account"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/health"
	reservationrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
)

// services are Credit's adapters over one set of domain services.
type services struct {
	rpc      router.RPCServices
	outcomes *events.Outcomes
	users    *events.Users
}

func newServices(pool *pgxpool.Pool) services {
	clock := time.Now

	accountStore := account.NewPostgresStore(pool)
	accounts := account.NewService(accountStore, accountStore, clock)
	reservations := reservation.NewService(reservation.NewPostgresRepository(pool), clock, reservation.LatenessPayout)

	return services{
		rpc: router.RPCServices{
			Health: &healthrpc.Checker{
				DB: pool,
				Services: []string{
					creditv1connect.CreditAccountServiceName,
					creditv1connect.CreditReservationServiceName,
				},
			},
			Account:     &accountrpc.Server{Accounts: accounts},
			Reservation: &reservationrpc.Server{Reservations: reservations},
		},
		outcomes: &events.Outcomes{Settler: reservations, Backoff: 100 * time.Millisecond},
		users:    &events.Users{Opener: accounts},
	}
}
