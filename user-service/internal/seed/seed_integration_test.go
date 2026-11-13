//go:build integration

package seed_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/seed"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

// TestSeedSuspensionFixture pins the suspension path end to end: the seeder
// applies the fixture suspension through the production role-change logic,
// whose session sweep requires the seeded RoleStore to carry the sessions
// dependency. A seed that skips the dependency panics mid-transaction.
func TestSeedSuspensionFixture(t *testing.T) {
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("seed_test"),
		postgres.WithUsername("seed"),
		postgres.WithPassword("seed"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	actorID := uuid.New()
	targetID := uuid.New()
	fixtures := seed.Fixtures{
		Version: 1,
		Users: []seed.User{
			{ID: actorID, Email: "actor@seed.test", DisplayName: "Actor", Role: models.RoleAdmin},
			{ID: targetID, Email: "target@seed.test", DisplayName: "Target", Role: models.RoleSuspended},
		},
		Domain: seed.Domain{ID: uuid.New(), Domain: "seed.test"},
		Suspension: seed.Suspension{
			UserID: targetID, ActorID: actorID, Reason: "seed fixture suspension",
		},
	}

	done := make(chan error, 1)
	go func() { done <- (seed.Seeder{DB: db}).Run(ctx, fixtures, false) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("seed with suspension fixture: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("seed did not finish; the suspension path likely panicked or deadlocked")
	}

	persistence := store.New(db)
	target, err := persistence.Users.GetByID(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if target.Role != models.RoleSuspended {
		t.Fatalf("fixture user role is %v, want suspended", target.Role)
	}
}
