//go:build integration

package location_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	r "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	d "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	"github.com/google/uuid"
)

func TestIndependentDisablementPostGIS(t *testing.T) {
	f := newFixture(t)
	f.reset(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	store := d.NewPostgresRepository(f.pool, clock)
	service := d.New(store, clock)
	caller := d.Caller{ID: "admin", Role: "admin"}
	start := now.Add(time.Hour)
	input := d.CreateDisablement{LocationID: postgresLocationID, StartsAt: &start, Reason: "maintenance", Key: uuid.NewString()}
	first, err := service.CreateDisablement(f.ctx, caller, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.CreateDisablement(f.ctx, caller, input)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	// Two independent connections compete on the same revision. Only one may commit.
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, reason := range []string{"first writer", "second writer"} {
		wg.Add(1)
		go func(reason string) {
			defer wg.Done()
			_, err := service.UpdateDisablement(f.ctx, caller, d.UpdateDisablement{ID: first.ID, ExpectedRevision: first.Revision, Reason: reason, Paths: []string{"reason"}})
			results <- err
		}(reason)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, d.ErrAborted) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("commits=%d conflicts=%d", successes, conflicts)
	}
	marker := errors.New("rollback after disablement write")
	err = store.Within(f.ctx, func(tx d.Tx) error {
		value, err := tx.Disablement(f.ctx, first.ID)
		if err != nil {
			return err
		}
		previous := value.Revision
		value.Revision++
		value.Reason = "must roll back"
		if err := tx.SaveDisablement(f.ctx, value, previous); err != nil {
			return err
		}
		return marker
	})
	if err != marker {
		t.Fatalf("callback error identity=%v", err)
	}
	page, err := service.ListDisablements(f.ctx, caller, postgresLocationID, "", d.Page{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Revision != 2 || page.Items[0].Reason == "must roll back" {
		t.Fatalf("rollback=%+v err=%v", page, err)
	}
	// AdditionRequest reads the same Location snapshot without importing Disablement's service.
	active := now.Add(-time.Minute)
	cancelled, err := service.CancelDisablement(f.ctx, caller, first.ID)
	if err != nil || cancelled.State(now) != d.Cancelled {
		t.Fatalf("cancel=%+v err=%v", cancelled, err)
	}
	input.StartsAt = &active
	input.Key = uuid.NewString()
	current, err := service.CreateDisablement(f.ctx, caller, input)
	if err != nil {
		t.Fatal(err)
	}
	err = r.NewPostgresRepository(f.pool, clock).Within(f.ctx, func(tx r.Tx) error {
		location, err := tx.Location(f.ctx, postgresLocationID)
		if err == nil && (location.CurrentDisablement == nil || location.CurrentDisablement.ID != current.ID) {
			t.Fatalf("current warning=%+v", location)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
