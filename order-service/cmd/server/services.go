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

func newRPCServices(pool *pgxpool.Pool, creator *order.Creator, images *order.ItemImages, assignments orderrpc.AssignmentOperations, cancellations orderrpc.CancellationOperations) router.RPCServices {
	repository := order.NewPostgresRepository(pool)
	service := order.NewService(repository)
	progress := order.NewProgress(order.NewMutator(repository))

	return router.RPCServices{
		Health: &healthrpc.Server{DB: pool},
		Order:  orderrpc.NewServerWithCancellation(service, creator, images, assignments, cancellations, progress),
		Admin:  adminrpc.NewServer(service, images),
	}
}

// Configuration is checked locally; Credit reachability is deliberately not a
// startup dependency. Recovery must remain configured while holds are unresolved.
func newCreator(ctx context.Context, repository order.CreationRepository, c config, images *order.ItemImages) (*order.Creator, bool, error) {
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
			return nil, false, errors.New("CREDIT_WORKLOAD_TOKEN_FILE must contain a readable nonempty bearer credential")
		}
		credit = &clients.Credit{Client: creditv1connect.NewCreditReservationServiceClient(client, c.CreditServiceURL), Tokens: clients.FileTokens{Path: c.CreditTokenFile}}
	}
	var locations order.CreationLocations
	if c.SupplierServiceURL != "" {
		locations = &clients.Supplier{Client: locationv1connect.NewLocationDiscoveryServiceClient(client, c.SupplierServiceURL)}
	}
	return order.NewCreator(repository, locations, credit, order.CreationOptions{AdmissionsEnabled: credit != nil && locations != nil, Images: images}), credit != nil, nil
}
