package main

import (
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/health"
	orderrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderadmin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRPCServices(pool *pgxpool.Pool) router.RPCServices {
	service := order.NewService(order.NewPostgresRepository(pool))

	return router.RPCServices{
		Health: &healthrpc.Server{DB: pool},
		Order:  orderrpc.NewServer(service),
		Admin:  adminrpc.NewServer(service),
	}
}
