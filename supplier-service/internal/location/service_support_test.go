package location_test

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	r "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	d "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

// This test-only composition retains cross-capability regressions without a production facade.
type testTx interface {
	r.Tx
	Disablement(context.Context, string) (d.Disablement, error)
	SaveDisablement(context.Context, d.Disablement, int64) error
	Overlaps(context.Context, d.Disablement) (bool, error)
	ListDisablements(context.Context, string, d.DisablementState, time.Time, d.Page) ([]d.Disablement, int64, error)
}
type testRepository interface {
	Within(context.Context, func(testTx) error) error
}
type requestRepository struct{ repo testRepository }

func (a requestRepository) Within(ctx context.Context, f func(r.Tx) error) error {
	return a.repo.Within(ctx, func(tx testTx) error { return f(tx) })
}

type disablementRepository struct{ repo testRepository }

func (a disablementRepository) Within(ctx context.Context, f func(d.Tx) error) error {
	return a.repo.Within(ctx, func(tx testTx) error { return f(disablementTx{tx}) })
}

type disablementTx struct{ testTx }

func (a disablementTx) Location(ctx context.Context, id string) (d.Location, error) {
	l, e := a.testTx.Location(ctx, id)
	return d.Location{ID: l.ID, ArchivedAt: l.ArchivedAt}, e
}

type disablementService = d.Service
type requestService = r.Service
type testService struct {
	*disablementService
	*requestService
}

func newTestService(repo testRepository, clock func() time.Time) *testService {
	return &testService{d.New(disablementRepository{repo}, clock), r.New(requestRepository{repo}, clock)}
}

var _ idempotency.Store = (*TestRepository)(nil)
