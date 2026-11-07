package httpauth

import (
	"context"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	revocationDSNOnce sync.Once
	revocationDSNVal  string
	revocationDSNErr  error
)

// revocationDSN returns the DSN of one throwaway PostgreSQL shared by the
// package's tests. NewStore dials eagerly, so tests exercising the real
// constructor cannot point the revocation store at a fake address.
func revocationDSN(t *testing.T) string {
	t.Helper()
	revocationDSNOnce.Do(func() {
		ctx := context.Background()
		container, err := postgres.Run(ctx, "postgres:18-alpine",
			postgres.WithDatabase("revocation_test"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			revocationDSNErr = err
			return
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			revocationDSNErr = err
			return
		}
		revocationDSNVal = dsn
	})
	if revocationDSNErr != nil {
		t.Fatalf("start shared revocation postgres: %v", revocationDSNErr)
	}
	return revocationDSNVal
}
