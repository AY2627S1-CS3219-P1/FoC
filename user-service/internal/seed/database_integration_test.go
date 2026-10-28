//go:build integration

package seed

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"gorm.io/gorm"
)

func testSeeder(db *gorm.DB) Seeder {
	persistence := store.New(db)
	return Seeder{WithTransaction: func(ctx context.Context, operation func(Transaction) error) error {
		return persistence.WithFixtureTransaction(ctx, func(tx *store.Store) error {
			return operation(Transaction{Users: tx.Users, Events: tx.UserCreatedOutbox, Fixtures: tx.Fixtures})
		})
	}}
}

// Use disposable databases: schema setup is destructive.
func testPostgres(t *testing.T, ctx context.Context) (*gorm.DB, string) {
	t.Helper()
	container := testContainer(t, ctx, "postgres:18", []string{
		"-e", "POSTGRES_PASSWORD=seed-test-only", "-e", "POSTGRES_DB=users_seed_test",
		"-p", "127.0.0.1::5432",
	})
	address := strings.TrimSpace(docker(t, ctx, "port", container, "5432/tcp"))
	dsn := "postgres://postgres:seed-test-only@" + address + "/users_seed_test?sslmode=disable"
	eventually(t, ctx, func() bool {
		return exec.CommandContext(ctx, "docker", "exec", container,
			"pg_isready", "-h", "127.0.0.1", "-U", "postgres").Run() == nil
	})
	db, err := database.Open(dsn, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

func testContainer(t *testing.T, ctx context.Context, image string, options []string, command ...string) string {
	t.Helper()
	args := append([]string{"run", "--rm", "-d"}, options...)
	args = append(args, image)
	args = append(args, command...)
	container := strings.TrimSpace(docker(t, ctx, args...))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(cleanupCtx, "docker", "rm", "-f", container).
			CombinedOutput(); err != nil {
			t.Errorf("remove test container: %v: %s", err, output)
		}
	})
	return container
}

func docker(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v: %s", args, err, output)
	}
	return string(output)
}

func eventually(t *testing.T, ctx context.Context, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !predicate() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatal("timed out waiting for integration condition")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatal(err)
	}
}

func scalar(t *testing.T, db *gorm.DB, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := db.Raw(sql, args...).Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	return value
}

func requireCount(t *testing.T, db *gorm.DB, table string, expected int) {
	t.Helper()
	if got := scalar(t, db, "SELECT count(*)::text FROM "+table); got != fmt.Sprint(expected) {
		t.Fatalf("%s count=%s, want %d", table, got, expected)
	}
}
