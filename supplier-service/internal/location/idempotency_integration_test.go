//go:build integration

package location_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	"github.com/google/uuid"
)

func TestWorkflowIdempotencyPostGIS(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expired := now.Add(-time.Hour)
	const requestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repository := additionrequest.NewPostgresRepository(f.pool, func() time.Time { return now })
	insert := func(t *testing.T, scope idempotency.Scope, expiresAt time.Time) {
		t.Helper()
		f.exec(t, "INSERT INTO supplier_idempotency(caller_id,method,key,request_hash,resource_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)", scope.Caller, scope.Method, scope.Key, requestHash, uuid.NewString(), expiresAt)
	}

	t.Run("expired cleanup preserves other scopes and active keys", func(t *testing.T) {
		f.reset(t)
		scope := idempotency.Scope{Caller: "owner", Method: "submit", Key: uuid.NewString()}
		others := []idempotency.Scope{
			{Caller: "other", Method: scope.Method, Key: scope.Key},
			{Caller: scope.Caller, Method: "create", Key: scope.Key},
			{Caller: scope.Caller, Method: scope.Method, Key: uuid.NewString()},
		}
		insert(t, scope, expired)
		for _, other := range others {
			insert(t, other, expired)
		}
		active := idempotency.Scope{Caller: scope.Caller, Method: scope.Method, Key: uuid.NewString()}
		insert(t, active, now.Add(time.Hour))
		if err := repository.Within(f.ctx, func(tx additionrequest.Tx) error {
			if err := tx.Idempotency().Lock(f.ctx, scope); err != nil {
				return err
			}
			record, err := tx.Idempotency().Find(f.ctx, scope, now)
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
		if err := repository.Within(f.ctx, func(tx additionrequest.Tx) error {
			if err := tx.Idempotency().Lock(f.ctx, active); err != nil {
				return err
			}
			record, err := tx.Idempotency().Find(f.ctx, active, now)
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
		first := idempotency.Scope{Caller: "owner", Method: "submit", Key: uuid.NewString()}
		second := idempotency.Scope{Caller: "other", Method: first.Method, Key: first.Key}
		insert(t, first, expired)
		insert(t, second, expired)
		ready := make(chan error, 1)
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- repository.Within(f.ctx, func(tx additionrequest.Tx) error {
				if err := tx.Idempotency().Lock(f.ctx, first); err != nil {
					return err
				}
				record, err := tx.Idempotency().Find(f.ctx, first, now)
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
		if err := repository.Within(ctx, func(tx additionrequest.Tx) error {
			if err := tx.Idempotency().Lock(ctx, second); err != nil {
				return err
			}
			record, err := tx.Idempotency().Find(ctx, second, now)
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
	t.Run("shared retry save constraint retains code and rolls back Location", func(t *testing.T) {
		f.reset(t)
		scope := idempotency.Scope{Caller: "owner", Method: "submit", Key: uuid.NewString()}
		runner := idempotency.New(func() time.Time { return now })
		var createdID string
		err := repository.Within(f.ctx, func(tx additionrequest.Tx) error {
			_, err := runner.Run(f.ctx, tx.Idempotency(), scope, "invalid hash", func(createdAt time.Time) (string, error) {
				location, err := tx.CreateLocation(f.ctx, additionrequest.Proposal{Name: "Rolled back retry", BuildingID: postgresBuildingID, Latitude: 1.294, Longitude: 103.774}, createdAt)
				createdID = location.ID
				return location.ID, err
			})
			return err
		})
		if createdID == "" || !errors.Is(err, additionrequest.ErrFailedPrecondition) {
			t.Fatalf("createdID=%s error=%v", createdID, err)
		}
		var locations, keys int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM locations WHERE id=$1", createdID).Scan(&locations); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM supplier_idempotency").Scan(&keys); err != nil {
			t.Fatal(err)
		}
		if locations != 0 || keys != 0 {
			t.Fatalf("retry save did not roll back: locations=%d keys=%d", locations, keys)
		}
	})

}
