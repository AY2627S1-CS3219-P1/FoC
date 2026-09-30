//go:build integration

package lifecycle_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	workflowrepo "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	workflows "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"github.com/google/uuid"
)

func TestWorkflowIdempotencyPostGIS(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expired := now.Add(-time.Hour)
	const requestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repository := workflowrepo.NewPostgresRepository(f.pool, func() time.Time { return now })
	insert := func(t *testing.T, scope workflows.IdempotencyScope, expiresAt time.Time) {
		t.Helper()
		f.exec(t, "INSERT INTO supplier_idempotency(caller_id,method,key,request_hash,resource_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)", scope.Caller, scope.Method, scope.Key, requestHash, uuid.NewString(), expiresAt)
	}

	t.Run("expired cleanup preserves other scopes and active keys", func(t *testing.T) {
		f.reset(t)
		scope := workflows.IdempotencyScope{Caller: "owner", Method: "submit", Key: uuid.NewString()}
		others := []workflows.IdempotencyScope{
			{Caller: "other", Method: scope.Method, Key: scope.Key},
			{Caller: scope.Caller, Method: "create", Key: scope.Key},
			{Caller: scope.Caller, Method: scope.Method, Key: uuid.NewString()},
		}
		insert(t, scope, expired)
		for _, other := range others {
			insert(t, other, expired)
		}
		active := workflows.IdempotencyScope{Caller: scope.Caller, Method: scope.Method, Key: uuid.NewString()}
		insert(t, active, now.Add(time.Hour))
		if err := repository.Within(f.ctx, func(tx workflows.Tx) error {
			record, err := tx.Idempotency(f.ctx, scope, now)
			if err == nil && record != nil {
				return fmt.Errorf("expired retry was replayed")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, other := range append(others, active) {
			var count int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM supplier_idempotency WHERE caller_id=$1 AND method=$2 AND key=$3", other.Caller, other.Method, other.Key).Scan(&count); err != nil || count != 1 {
				t.Fatalf("cleanup changed another retry scope: scope=%+v count=%d error=%v", other, count, err)
			}
		}
		if err := repository.Within(f.ctx, func(tx workflows.Tx) error {
			record, err := tx.Idempotency(f.ctx, active, now)
			if err == nil && (record == nil || record.Hash != requestHash) {
				return fmt.Errorf("cleanup lost an active retry")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("expired cleanup does not block another scope", func(t *testing.T) {
		f.reset(t)
		first := workflows.IdempotencyScope{Caller: "owner", Method: "submit", Key: uuid.NewString()}
		second := workflows.IdempotencyScope{Caller: "other", Method: first.Method, Key: first.Key}
		insert(t, first, expired)
		insert(t, second, expired)
		ready := make(chan error, 1)
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- repository.Within(f.ctx, func(tx workflows.Tx) error {
				record, err := tx.Idempotency(f.ctx, first, now)
				if err == nil && record != nil {
					err = fmt.Errorf("expired retry was replayed")
				}
				ready <- err
				if err != nil {
					return err
				}
				<-release
				return nil
			})
		}()
		finished := false
		defer func() {
			close(release)
			if finished {
				return
			}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("first retry transaction: %v", err)
				}
			case <-f.ctx.Done():
				t.Errorf("first retry transaction did not finish: %v", f.ctx.Err())
			}
		}()
		select {
		case err := <-ready:
			if err != nil {
				t.Fatal(err)
			}
		case err := <-done:
			finished = true
			t.Fatalf("first retry transaction did not reach readiness: %v", err)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		if err := repository.Within(ctx, func(tx workflows.Tx) error {
			record, err := tx.Idempotency(ctx, second, now)
			if err == nil && record != nil {
				return fmt.Errorf("expired retry was replayed")
			}
			return err
		}); err != nil {
			t.Fatalf("independent scope cleanup waited behind another transaction: %v", err)
		}
		var count int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM supplier_idempotency WHERE caller_id=$1 AND method=$2 AND key=$3", first.Caller, first.Method, first.Key).Scan(&count); err != nil || count != 1 {
			t.Fatalf("first transaction did not remain uncommitted: count=%d error=%v", count, err)
		}
	})
}
