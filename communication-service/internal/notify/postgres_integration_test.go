package notify

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
)

// TestPostgresStartKeepsTheFirstTime needs TEST_DATABASE_URL pointing at a
// throwaway database, because it clears the start time.
func TestPostgresStartKeepsTheFirstTime(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE notifications_start`); err != nil {
		t.Fatal(err)
	}
	d := &PostgresDeliveries{Queries: db.New(pool)}

	first := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{first, first.Add(time.Hour)} {
		got, err := d.Start(ctx, at)
		if err != nil || !got.Equal(first) {
			t.Fatalf("start = %v, %v; want %v", got, err, first)
		}
	}
}
