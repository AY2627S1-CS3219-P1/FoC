package disablement_test

import (
	"context"
	"errors"

	"testing"
	"time"

	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

func TestScheduledDisablementCannotEndEarly(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	app := newTestService(repo, func() time.Time { return now })
	d, err := app.CreateDisablement(context.Background(), admin, disablement.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), Reason: "maintenance", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.EndDisablement(context.Background(), admin, d.ID)
	if !errors.Is(err, disablement.ErrFailedPrecondition) {
		t.Fatalf("expected failed precondition, got %v", err)
	}
}

func TestDisablementLifecycleRetriesRevisionsAndOverlap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	app := newTestService(repo, func() time.Time { return now })
	in := disablement.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), EndsAt: ptr(now.Add(3 * time.Hour)), Reason: " maintenance ", Key: key}
	d, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	if d.Reason != "maintenance" || d.State(now) != disablement.Scheduled {
		t.Fatalf("unexpected record: %+v", d)
	}
	same, e := app.CreateDisablement(ctx, admin, in)
	if e != nil || same.ID != d.ID {
		t.Fatalf("retry: %+v %v", same, e)
	}
	in.Reason = "other"
	_, e = app.CreateDisablement(ctx, admin, in)
	expectError(t, e, disablement.ErrAlreadyExists)
	in.Key = "7ca52cce-643c-44c3-b0e4-1a998b4cbbca"
	_, e = app.CreateDisablement(ctx, admin, in)
	expectError(t, e, disablement.ErrAlreadyExists)
	updated, e := app.UpdateDisablement(ctx, admin, disablement.UpdateDisablement{ID: d.ID, Reason: "changed", Paths: []string{"reason"}, ExpectedRevision: 1})
	if e != nil || updated.Revision != 2 || !updated.StartsAt.Equal(d.StartsAt) {
		t.Fatalf("update: %+v %v", updated, e)
	}
	_, e = app.UpdateDisablement(ctx, admin, disablement.UpdateDisablement{ID: d.ID, Reason: "stale", Paths: []string{"reason"}, ExpectedRevision: 1})
	expectError(t, e, disablement.ErrAborted)
	cancelled, e := app.CancelDisablement(ctx, admin, d.ID)
	if e != nil || cancelled.State(now) != disablement.Cancelled {
		t.Fatalf("cancel: %+v %v", cancelled, e)
	}
	retry, e := app.CancelDisablement(ctx, admin, d.ID)
	if e != nil || retry.Revision != cancelled.Revision {
		t.Fatalf("cancel retry: %+v %v", retry, e)
	}
	_, e = app.EndDisablement(ctx, admin, d.ID)
	expectError(t, e, disablement.ErrFailedPrecondition)
	// Cancellation frees the original interval for a distinct creation.
	created, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Hour)
	_, e = app.CancelDisablement(ctx, admin, created.ID)
	expectError(t, e, disablement.ErrFailedPrecondition)
	_, e = app.UpdateDisablement(ctx, admin, disablement.UpdateDisablement{ID: created.ID, Reason: "too late", Paths: []string{"reason"}, ExpectedRevision: 1})
	expectError(t, e, disablement.ErrFailedPrecondition)
	ended, e := app.EndDisablement(ctx, admin, created.ID)
	if e != nil || ended.EndedAt == nil || ended.State(now) != disablement.Ended {
		t.Fatalf("end: %+v %v", ended, e)
	}
	retry, e = app.EndDisablement(ctx, admin, created.ID)
	if e != nil || retry.Revision != ended.Revision {
		t.Fatalf("end retry: %+v %v", retry, e)
	}
	_, e = app.CancelDisablement(ctx, admin, created.ID)
	expectError(t, e, disablement.ErrFailedPrecondition)
	// Adjacent intervals do not overlap. The ended historical portion still does.
	_, e = app.CreateDisablement(ctx, admin, disablement.CreateDisablement{LocationID: locationID, StartsAt: ptr(now), Reason: "next", Key: "d27f9a93-b9fc-4dd4-9d2d-abd297866dba"})
	if e != nil {
		t.Fatal(e)
	}
	list, e := app.ListDisablements(ctx, admin, locationID, disablement.Ended, disablement.Page{})
	if e != nil || len(list.Items) != 1 || list.PageInfo.Size != 20 {
		t.Fatalf("ended list: %+v %v", list, e)
	}
}

func TestImmediateDisablementRetryDoesNotUseNewClockValue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	app := newTestService(repo, func() time.Time { return now })
	in := disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key}
	first, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Hour)
	retry, e := app.CreateDisablement(ctx, admin, in)
	if e != nil || retry.ID != first.ID || !retry.StartsAt.Equal(first.StartsAt) {
		t.Fatalf("default-start retry: %+v %v", retry, e)
	}
}

func TestScheduledUpdateMayStartNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = disablement.Location{ID: locationID}
	app := newTestService(repo, func() time.Time { return now })
	d, e := app.CreateDisablement(ctx, admin, disablement.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), Reason: "maintenance", Key: key})
	if e != nil {
		t.Fatal(e)
	}
	updated, e := app.UpdateDisablement(ctx, admin, disablement.UpdateDisablement{ID: d.ID, StartsAt: &now, Paths: []string{"starts_at"}, ExpectedRevision: 1})
	if e != nil || updated.State(now) != disablement.Active {
		t.Fatalf("start now: %+v %v", updated, e)
	}
}
