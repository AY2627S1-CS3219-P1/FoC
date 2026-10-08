//go:build integration

package location_test

import (
	"time"

	r "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	d "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	"github.com/jackc/pgx/v5/pgxpool"
)

type disablementService = d.Service
type requestService = r.Service
type testDomainServices struct {
	*disablementService
	*requestService
}

func newTestDomainServices(pool *pgxpool.Pool, clock func() time.Time) *testDomainServices {
	return &testDomainServices{d.NewService(d.NewPostgresRepository(pool, clock), clock), r.NewService(r.NewPostgresRepository(pool, clock), clock)}
}
