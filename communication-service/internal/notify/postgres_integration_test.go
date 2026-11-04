package notify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// openPostgres needs TEST_DATABASE_URL pointing at a throwaway database,
// because the tests clear notification tables and rerun migrations.
func openPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPostgresStartKeepsTheFirstTime(t *testing.T) {
	pool := openPostgres(t)
	ctx := context.Background()
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

// The start-time migration must not skip events waiting during an upgrade of
// an installation that already handled orders. Each case migrates a fresh
// schema, so it doesn't disturb other packages' tests on the same database.
func TestStartMigrationKeepsExistingInstallationsComplete(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	upgrade := func(t *testing.T, existing bool) *PostgresDeliveries {
		t.Helper()
		admin := openPostgres(t)
		schema := "start_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		sqlDB := stdlib.OpenDBFromPool(pool)
		t.Cleanup(func() { _ = sqlDB.Close() })
		provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.UpTo(ctx, 3); err != nil {
			t.Fatal(err)
		}
		if existing {
			if _, err := pool.Exec(ctx, `INSERT INTO order_notifications_sent (event_id, user_id, sent_at)
				VALUES ($1, $2, now())`, uuid.New(), uuid.New()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
		return &PostgresDeliveries{Queries: db.New(pool)}
	}
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

	t.Run("existing installation skips nothing", func(t *testing.T) {
		got, err := upgrade(t, true).Start(ctx, now)
		if err != nil || !got.Equal(time.Unix(0, 0)) {
			t.Fatalf("start = %v, %v", got, err)
		}
	})
	t.Run("new installation starts now", func(t *testing.T) {
		got, err := upgrade(t, false).Start(ctx, now)
		if err != nil || !got.Equal(now) {
			t.Fatalf("start = %v, %v", got, err)
		}
	})
}
