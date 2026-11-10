package users

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/google/uuid"
)

// Needs TEST_DATABASE_URL pointing at a throwaway database.
func TestPostgresKeepsTheFirstEmail(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &PostgresStore{Queries: db.New(pool)}
	id := uuid.New()
	for _, address := range []string{"alice@foc.local", "replayed@foc.local"} {
		if err := s.SaveEmail(ctx, id, address, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Email(ctx, id); err != nil || got != "alice@foc.local" {
		t.Fatalf("email = %q, %v", got, err)
	}
	if _, err := s.Email(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}
