package main

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	locationadmin "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	locationdiscovery "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newServices(pool *pgxpool.Pool) router.Services {
	return router.Services{
		LocationAdmin: locationadmin.NewService(
			locationadmin.NewPostgresStore(pool),
			time.Now,
		),
		LocationDiscovery: locationdiscovery.NewService(
			locationdiscovery.NewPostgresReader(locationdb.New(pool)),
		),
	}
}
