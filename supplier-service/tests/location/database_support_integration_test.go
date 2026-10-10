//go:build integration

package location_test

import (
	"context"
	"database/sql"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/locationfixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

const (
	postgresBuildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	postgresCategoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	otherCategoryID    = "d064dc96-f8be-47ef-a1a3-9c72c05e708c"
	postgresLocationID = "8f383cca-1609-4326-8440-f75d943dc924"
)

type fixture struct {
	*locationfixture.WorkflowDatabase
	ctx        context.Context
	pool       *pgxpool.Pool
	db         *sql.DB
	migrations string
}

func newFixture(t *testing.T) *fixture {
	f := locationfixture.NewWorkflowDatabase(t)
	return &fixture{WorkflowDatabase: f, ctx: f.Context, pool: f.Pool, db: f.DB, migrations: f.Migrations}
}

func (f *fixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	f.Exec(t, statement, args...)
}
func (f *fixture) reset(t *testing.T) {
	t.Helper()
	f.Reset(t)
}
