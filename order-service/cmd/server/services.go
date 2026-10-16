package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/health"
	orderrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderadmin"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRPCServices(pool *pgxpool.Pool, creator *order.Creator) router.RPCServices {
	service := order.NewService(order.NewPostgresRepository(pool))

	return router.RPCServices{
		Health: &healthrpc.Server{DB: pool},
		Order:  orderrpc.NewServer(service, creator),
		Admin:  adminrpc.NewServer(service),
	}
}

// Configuration is checked locally; Credit reachability is deliberately not a
// startup dependency. Recovery must remain configured while holds are unresolved.
func newCreator(ctx context.Context, repository order.CreationRepository, c config) (*order.Creator, bool, error) {
	pending, err := repository.HasPendingCreations(ctx)
	if err != nil {
		return nil, false, err
	}
	if pending && c.CreditServiceURL == "" {
		return nil, false, errors.New("outstanding creation workflows require Credit URL and token file for recovery")
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: clients.NoRedirect}
	var credit order.CreationCredit
	if c.CreditServiceURL != "" {
		if _, err := clients.ReadServiceToken(c.CreditTokenFile); err != nil {
			return nil, false, errors.New("CREDIT_SERVICE_TOKEN_FILE must contain a readable nonempty bearer credential")
		}
		credit = &clients.Credit{Client: creditv1connect.NewCreditReservationServiceClient(client, c.CreditServiceURL), TokenFile: c.CreditTokenFile}
	}
	var locations order.CreationLocations
	if c.SupplierServiceURL != "" {
		locations = &clients.Supplier{Client: locationv1connect.NewLocationDiscoveryServiceClient(client, c.SupplierServiceURL)}
	}
	return order.NewCreator(repository, locations, credit, order.CreationOptions{AdmissionsEnabled: c.CreationEnabled}), credit != nil, nil
}
