package main

import (
	"context"
	"time"

	"buf.build/go/protovalidate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	admin "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	discovery "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	domain "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/health"
	additionrequestrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/additionrequest"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/admin"
	disablementrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/disablement"
	discoveryrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/discovery"
	"github.com/jackc/pgx/v5/pgxpool"
)

type domainServices struct {
	admin           *admin.Service
	discovery       *discovery.Service
	disablement     *disablement.Service
	additionRequest *additionrequest.Service
}

func newServices(pool *pgxpool.Pool, clock func() time.Time) domainServices {
	return domainServices{
		admin:           admin.NewService(admin.NewPostgresStore(pool), clock),
		discovery:       discovery.NewService(discovery.NewPostgresReader(locationdb.New(pool))),
		disablement:     disablement.NewService(disablement.NewPostgresRepository(pool, clock), clock),
		additionRequest: additionrequest.NewService(additionrequest.NewPostgresRepository(pool, clock), clock),
	}
}
func newRPCServices(pool *pgxpool.Pool, clock func() time.Time) (router.RPCServices, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return router.RPCServices{}, err
	}
	principal := func(ctx context.Context) domain.Caller {
		caller, ok := auth.CallerFromContext(ctx)
		if !ok {
			return domain.Caller{}
		}
		return domain.Caller{ID: caller.ID, Role: caller.Role}
	}
	services := newServices(pool, clock)
	disablementHandler := disablementrpc.NewServer(services.disablement, principal, clock)
	additionRequestHandler := additionrequestrpc.NewServer(services.additionRequest, principal, clock)
	return router.RPCServices{
		Health:      healthrpc.NewServer(),
		Discovery:   discoveryrpc.NewServer(services.discovery),
		Admin:       adminrpc.NewServer(services.admin),
		Disablement: disablementHandler, AdditionRequest: additionRequestHandler,
		DisablementInterceptor:     disablementrpc.CallerInterceptor(principal, validator),
		AdditionRequestInterceptor: additionrequestrpc.CallerInterceptor(principal, validator),
	}, nil
}
