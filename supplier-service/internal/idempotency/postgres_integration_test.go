//go:build integration

package idempotency_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const goodHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type runResult struct {
	id  string
	err error
}

func waitForAdvisoryLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
            SELECT 1 FROM pg_stat_activity
            WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
              AND query LIKE '%pg_advisory_xact_lock%'
        )`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("second transaction did not wait on the idempotency advisory lock")
}

func postgresFixture(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	options := []testcontainers.ContainerCustomizer{postgres.WithDatabase("idempotency_test"), postgres.WithUsername("supplier"), postgres.WithPassword("supplier"), postgres.BasicWaitStrategies()}
	if runtime.GOARCH == "arm64" {
		options = append(options, testcontainers.WithImagePlatform("linux/amd64"))
	}
	container, err := postgres.Run(ctx, "postgis/postgis:18-3.6", options...)
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.CleanupContainer(t, container)
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, file, _, _ := runtime.Caller(0)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, filepath.Join(filepath.Dir(file), "../../database/schema")); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "CREATE TABLE test_resources (id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

func within(ctx context.Context, pool *pgxpool.Pool, fn func(*idempotency.PostgresStore, func(string) error) (string, error)) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	store := idempotency.NewPostgresStore(tx)
	resource := func(id string) error {
		_, err := tx.Exec(ctx, "INSERT INTO test_resources(id) VALUES($1)", id)
		return err
	}
	result, err := fn(store, resource)
	if err != nil {
		return "", err
	}
	return result, tx.Commit(ctx)
}

func TestPostgresStoreTransactions(t *testing.T) {
	ctx, pool := postgresFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	runner := idempotency.New(func() time.Time { return now })
	count := func(table string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	reset := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, "TRUNCATE supplier_idempotency,test_resources"); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("concurrent same key replays identical hash", func(t *testing.T) {
		reset()
		scope := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		entered := make(chan struct{})
		release := make(chan struct{})
		first := make(chan runResult, 1)
		releaseFirst := sync.OnceFunc(func() { close(release) })
		defer releaseFirst()
		var calls atomic.Int32
		go func() {
			id, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
				return runner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
					calls.Add(1)
					close(entered)
					<-release
					id := uuid.NewString()
					return id, resource(id)
				})
			})
			first <- runResult{id, err}
		}()
		select {
		case <-entered:
		case result := <-first:
			t.Fatalf("first transaction stopped before callback: %v", result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		second := make(chan runResult, 1)
		go func() {
			id, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
				return runner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
					calls.Add(1)
					id := uuid.NewString()
					return id, resource(id)
				})
			})
			second <- runResult{id, err}
		}()
		waitForAdvisoryLock(t, ctx, pool)
		releaseFirst()
		var a, b runResult
		select {
		case a = <-first:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		select {
		case b = <-second:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if a.err != nil || b.err != nil || a.id != b.id || calls.Load() != 1 || count("test_resources") != 1 || count("supplier_idempotency") != 1 {
			t.Fatalf("replay a=%+v b=%+v callbacks=%d resources=%d keys=%d", a, b, calls.Load(), count("test_resources"), count("supplier_idempotency"))
		}
	})

	t.Run("concurrent same key rejects different hash", func(t *testing.T) {
		reset()
		scope := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		entered := make(chan struct{})
		release := make(chan struct{})
		releaseFirst := sync.OnceFunc(func() { close(release) })
		defer releaseFirst()
		first := make(chan error, 1)
		go func() {
			_, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
				return runner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
					close(entered)
					<-release
					id := uuid.NewString()
					return id, resource(id)
				})
			})
			first <- err
		}()
		select {
		case <-entered:
		case err := <-first:
			t.Fatalf("first transaction stopped before callback: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		second := make(chan error, 1)
		go func() {
			_, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
				return runner.Run(ctx, store, scope, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", func(time.Time) (string, error) {
					t.Error("conflicting retry invoked callback")
					return "", nil
				})
			})
			second <- err
		}()
		waitForAdvisoryLock(t, ctx, pool)
		releaseFirst()
		select {
		case err := <-first:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		select {
		case err := <-second:
			if !errors.Is(err, idempotency.ErrConflict) {
				t.Fatalf("conflicting retry error = %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if count("test_resources") != 1 || count("supplier_idempotency") != 1 {
			t.Fatal("conflicting retry changed rows")
		}
	})

	t.Run("callback failure rolls back resource and permits retry", func(t *testing.T) {
		reset()
		scope := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		failure := errors.New("callback failed")
		_, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
			return runner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
				if err := resource(uuid.NewString()); err != nil {
					return "", err
				}
				return "", failure
			})
		})
		if !errors.Is(err, failure) || count("test_resources") != 0 || count("supplier_idempotency") != 0 {
			t.Fatalf("callback rollback error=%v resources=%d keys=%d", err, count("test_resources"), count("supplier_idempotency"))
		}
		_, err = within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
			return runner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
				id := uuid.NewString()
				return id, resource(id)
			})
		})
		if err != nil || count("test_resources") != 1 || count("supplier_idempotency") != 1 {
			t.Fatalf("retry error=%v resources=%d keys=%d", err, count("test_resources"), count("supplier_idempotency"))
		}
	})

	t.Run("save failure rolls back resource and key", func(t *testing.T) {
		reset()
		scope := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		_, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
			return runner.Run(ctx, store, scope, "invalid hash", func(time.Time) (string, error) {
				id := uuid.NewString()
				return id, resource(id)
			})
		})
		if err == nil || count("test_resources") != 0 || count("supplier_idempotency") != 0 {
			t.Fatalf("save rollback error=%v resources=%d keys=%d", err, count("test_resources"), count("supplier_idempotency"))
		}
	})

	t.Run("expiry is checked after waiting for the lock", func(t *testing.T) {
		reset()
		scope := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		if _, err := pool.Exec(ctx, "INSERT INTO supplier_idempotency(caller_id,method,key,request_hash,resource_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)", scope.Caller, scope.Method, scope.Key, goodHash, uuid.NewString(), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		var clockNanos atomic.Int64
		clockNanos.Store(now.UnixNano())
		movingRunner := idempotency.New(func() time.Time { return time.Unix(0, clockNanos.Load()).UTC() })
		entered := make(chan struct{})
		release := make(chan struct{})
		releaseFirst := sync.OnceFunc(func() { close(release) })
		defer releaseFirst()
		first := make(chan error, 1)
		go func() {
			_, err := within(ctx, pool, func(store *idempotency.PostgresStore, _ func(string) error) (string, error) {
				if err := store.Lock(ctx, scope); err != nil {
					return "", err
				}
				close(entered)
				<-release
				return "", nil
			})
			first <- err
		}()
		select {
		case <-entered:
		case err := <-first:
			t.Fatalf("lock holder stopped before readiness: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		second := make(chan runResult, 1)
		go func() {
			id, err := within(ctx, pool, func(store *idempotency.PostgresStore, resource func(string) error) (string, error) {
				return movingRunner.Run(ctx, store, scope, goodHash, func(time.Time) (string, error) {
					id := uuid.NewString()
					return id, resource(id)
				})
			})
			second <- runResult{id, err}
		}()
		waitForAdvisoryLock(t, ctx, pool)
		clockNanos.Store(now.Add(2 * time.Hour).UnixNano())
		releaseFirst()
		select {
		case err := <-first:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		select {
		case result := <-second:
			if result.err != nil || result.id == "" || count("test_resources") != 1 || count("supplier_idempotency") != 1 {
				t.Fatalf("expired retry result=%+v resources=%d keys=%d", result, count("test_resources"), count("supplier_idempotency"))
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})

	t.Run("expiry and independent scopes", func(t *testing.T) {
		reset()
		key := uuid.NewString()
		expired := idempotency.Scope{Caller: "owner", Method: "create", Key: key}
		otherCaller := idempotency.Scope{Caller: "other", Method: "create", Key: key}
		otherMethod := idempotency.Scope{Caller: "owner", Method: "update", Key: key}
		otherKey := idempotency.Scope{Caller: "owner", Method: "create", Key: uuid.NewString()}
		for _, scope := range []idempotency.Scope{expired, otherCaller, otherMethod, otherKey} {
			if _, err := pool.Exec(ctx, "INSERT INTO supplier_idempotency(caller_id,method,key,request_hash,resource_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)", scope.Caller, scope.Method, scope.Key, goodHash, uuid.NewString(), now.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		_, err := within(ctx, pool, func(store *idempotency.PostgresStore, _ func(string) error) (string, error) {
			if err := store.Lock(ctx, expired); err != nil {
				return "", err
			}
			record, err := store.Find(ctx, expired, now)
			if record != nil {
				t.Error("expired record replayed")
			}
			return "", err
		})
		if err != nil || count("supplier_idempotency") != 3 {
			t.Fatalf("scoped expiry error=%v keys=%d", err, count("supplier_idempotency"))
		}
	})
}
