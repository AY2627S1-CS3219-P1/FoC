package workflows_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
)

func TestScheduledDisablementCannotEndEarly(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	app := workflows.New(repo, func() time.Time { return now })
	d, err := app.CreateDisablement(context.Background(), admin, workflows.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), Reason: "maintenance", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.EndDisablement(context.Background(), admin, d.ID)
	if !errors.Is(err, workflows.ErrFailedPrecondition) {
		t.Fatalf("expected failed precondition, got %v", err)
	}
}

func TestDisablementLifecycleRetriesRevisionsAndOverlap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	app := workflows.New(repo, func() time.Time { return now })
	in := workflows.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), EndsAt: ptr(now.Add(3 * time.Hour)), Reason: " maintenance ", Key: key}
	d, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	if d.Reason != "maintenance" || d.State(now) != workflows.Scheduled {
		t.Fatalf("unexpected record: %+v", d)
	}
	same, e := app.CreateDisablement(ctx, admin, in)
	if e != nil || same.ID != d.ID {
		t.Fatalf("retry: %+v %v", same, e)
	}
	in.Reason = "other"
	_, e = app.CreateDisablement(ctx, admin, in)
	expectError(t, e, workflows.ErrAlreadyExists)
	in.Key = "7ca52cce-643c-44c3-b0e4-1a998b4cbbca"
	_, e = app.CreateDisablement(ctx, admin, in)
	expectError(t, e, workflows.ErrAlreadyExists)
	updated, e := app.UpdateDisablement(ctx, admin, workflows.UpdateDisablement{ID: d.ID, Reason: "changed", Paths: []string{"reason"}, ExpectedRevision: 1})
	if e != nil || updated.Revision != 2 || !updated.StartsAt.Equal(d.StartsAt) {
		t.Fatalf("update: %+v %v", updated, e)
	}
	_, e = app.UpdateDisablement(ctx, admin, workflows.UpdateDisablement{ID: d.ID, Reason: "stale", Paths: []string{"reason"}, ExpectedRevision: 1})
	expectError(t, e, workflows.ErrAborted)
	cancelled, e := app.CancelDisablement(ctx, admin, d.ID)
	if e != nil || cancelled.State(now) != workflows.Cancelled {
		t.Fatalf("cancel: %+v %v", cancelled, e)
	}
	retry, e := app.CancelDisablement(ctx, admin, d.ID)
	if e != nil || retry.Revision != cancelled.Revision {
		t.Fatalf("cancel retry: %+v %v", retry, e)
	}
	_, e = app.EndDisablement(ctx, admin, d.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
	// Cancellation frees the original interval for a distinct creation.
	created, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Hour)
	_, e = app.CancelDisablement(ctx, admin, created.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
	_, e = app.UpdateDisablement(ctx, admin, workflows.UpdateDisablement{ID: created.ID, Reason: "too late", Paths: []string{"reason"}, ExpectedRevision: 1})
	expectError(t, e, workflows.ErrFailedPrecondition)
	ended, e := app.EndDisablement(ctx, admin, created.ID)
	if e != nil || ended.EndedAt == nil || ended.State(now) != workflows.Ended {
		t.Fatalf("end: %+v %v", ended, e)
	}
	retry, e = app.EndDisablement(ctx, admin, created.ID)
	if e != nil || retry.Revision != ended.Revision {
		t.Fatalf("end retry: %+v %v", retry, e)
	}
	_, e = app.CancelDisablement(ctx, admin, created.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
	// Adjacent intervals do not overlap. The ended historical portion still does.
	_, e = app.CreateDisablement(ctx, admin, workflows.CreateDisablement{LocationID: locationID, StartsAt: ptr(now), Reason: "next", Key: "d27f9a93-b9fc-4dd4-9d2d-abd297866dba"})
	if e != nil {
		t.Fatal(e)
	}
	list, e := app.ListDisablements(ctx, admin, locationID, workflows.Ended, workflows.Page{})
	if e != nil || len(list.Items) != 1 || list.PageInfo.Size != 20 {
		t.Fatalf("ended list: %+v %v", list, e)
	}
}

func TestImmediateDisablementRetryDoesNotUseNewClockValue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	app := workflows.New(repo, func() time.Time { return now })
	in := workflows.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key}
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

func TestRequestApprovalVisibilityRedactionAndRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	app := workflows.New(repo, func() time.Time { return now })
	in := workflows.SubmitRequest{Proposal: validProposal(), Key: key}
	r, e := app.SubmitRequest(ctx, owner, in)
	if e != nil {
		t.Fatal(e)
	}
	other := workflows.Caller{ID: "other", Role: "user"}
	_, e = app.GetRequest(ctx, other, r.ID)
	expectError(t, e, workflows.ErrNotFound)
	list, e := app.ListRequests(ctx, other, "", workflows.Page{})
	if e != nil || list.PageInfo.TotalItems != 0 {
		t.Fatalf("hidden list: %+v %v", list, e)
	}
	updated, e := app.UpdateRequest(ctx, owner, workflows.UpdateRequest{ID: r.ID, Proposal: workflows.Proposal{Details: "updated"}, Paths: []string{"details"}, ExpectedRevision: 1})
	if e != nil || updated.Revision != 2 || updated.Proposal.Name != "Cafe" {
		t.Fatalf("patch: %+v %v", updated, e)
	}
	_, e = app.UpdateRequest(ctx, admin, workflows.UpdateRequest{ID: r.ID, Proposal: workflows.Proposal{Details: "stale"}, Paths: []string{"details"}, ExpectedRevision: 1})
	expectError(t, e, workflows.ErrAborted)
	result, e := app.ApproveRequest(ctx, admin, r.ID)
	if e != nil || result.Location.ID == "" || result.Request.Status != workflows.Approved || result.Location.Proposal.Details != "updated" {
		t.Fatalf("approval: %+v %v", result, e)
	}
	retry, e := app.ApproveRequest(ctx, admin, r.ID)
	if e != nil || retry.Location.ID != result.Location.ID || retry.Request.Revision != result.Request.Revision {
		t.Fatalf("approval retry: %+v %v", retry, e)
	}
	public, e := app.GetRequest(ctx, other, r.ID)
	if e != nil || public.SubmittedBy != "" || public.ReviewedBy != nil || public.ReviewNote != nil || public.ReviewedAt == nil {
		t.Fatalf("redaction: %+v %v", public, e)
	}
	full, e := app.GetRequest(ctx, owner, r.ID)
	if e != nil || full.SubmittedBy != "owner" || full.ReviewedBy == nil {
		t.Fatalf("owner projection: %+v %v", full, e)
	}
	list, e = app.ListRequests(ctx, other, workflows.Approved, workflows.Page{})
	if e != nil || list.PageInfo.TotalItems != 1 || list.Items[0].SubmittedBy != "" {
		t.Fatalf("public list: %+v %v", list, e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, "different transition")
	expectError(t, e, workflows.ErrFailedPrecondition)
	_, e = app.WithdrawRequest(ctx, owner, r.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
	_, e = app.UpdateRequest(ctx, owner, workflows.UpdateRequest{ID: r.ID, Paths: []string{"details"}, ExpectedRevision: 3})
	expectError(t, e, workflows.ErrFailedPrecondition)
}

func TestRequestWithdrawRejectAndApprovalRollback(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Now().UTC()
	app := workflows.New(repo, func() time.Time { return now })
	r, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	repo.failCreate = true
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	if e == nil {
		t.Fatal("expected dependency failure")
	}
	stored, e := app.GetRequest(ctx, owner, r.ID)
	if e != nil || stored.Status != workflows.Pending || stored.Revision != 1 {
		t.Fatalf("rollback: %+v %v", stored, e)
	}
	_, e = app.WithdrawRequest(ctx, admin, r.ID)
	expectError(t, e, workflows.ErrPermissionDenied)
	withdrawn, e := app.WithdrawRequest(ctx, owner, r.ID)
	if e != nil || withdrawn.Status != workflows.Withdrawn || withdrawn.ReviewedBy != nil {
		t.Fatalf("withdraw: %+v %v", withdrawn, e)
	}
	retry, e := app.WithdrawRequest(ctx, owner, r.ID)
	if e != nil || retry.Revision != withdrawn.Revision {
		t.Fatalf("withdraw retry: %+v %v", retry, e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, "note")
	expectError(t, e, workflows.ErrFailedPrecondition)
	r, e = app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: "c3600892-6029-4910-b1c9-d3e892039f63"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, " ")
	expectError(t, e, workflows.ErrInvalidArgument)
	rejected, e := app.RejectRequest(ctx, admin, r.ID, " not suitable ")
	if e != nil || rejected.Status != workflows.Rejected || *rejected.ReviewNote != "not suitable" {
		t.Fatalf("reject: %+v %v", rejected, e)
	}
	retry, e = app.RejectRequest(ctx, admin, r.ID, "new note")
	if e != nil || *retry.ReviewNote != "not suitable" {
		t.Fatalf("reject retry: %+v %v", retry, e)
	}
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
}

func TestApprovalRollsBackLocationWhenRequestSaveFails(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	app := workflows.New(repo, func() time.Time { return now })
	request, err := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if err != nil {
		t.Fatal(err)
	}

	repo.failSaveRequest = true
	_, err = app.ApproveRequest(ctx, admin, request.ID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("approval error: got %v, want injected save failure", err)
	}
	if repo.locationsAtFailedSave != 1 {
		t.Fatalf("locations before save failure: got %d, want 1", repo.locationsAtFailedSave)
	}
	if len(repo.locations) != 0 {
		t.Fatalf("rollback left %d orphaned Locations", len(repo.locations))
	}
	stored, err := app.GetRequest(ctx, owner, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != workflows.Pending || stored.Revision != 1 || stored.ResultingLocationID != nil || stored.ReviewedBy != nil || stored.ReviewedAt != nil || stored.ReviewNote != nil {
		t.Fatalf("rollback changed pending request: %+v", stored)
	}
}

func TestRequestIdempotencyScopesExpiryAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	app := workflows.New(repo, func() time.Time { return now })
	in := workflows.SubmitRequest{Proposal: validProposal(), Key: key}
	first, e := app.SubmitRequest(ctx, owner, in)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := app.SubmitRequest(ctx, owner, in)
	if e != nil || retry.ID != first.ID {
		t.Fatalf("retry: %+v %v", retry, e)
	}
	in.Proposal.Name = "different"
	_, e = app.SubmitRequest(ctx, owner, in)
	expectError(t, e, workflows.ErrAlreadyExists)
	other := workflows.Caller{ID: "another", Role: "user"}
	_, e = app.SubmitRequest(ctx, other, in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(24 * time.Hour)
	next, e := app.SubmitRequest(ctx, owner, in)
	if e != nil || next.ID == first.ID {
		t.Fatalf("expiry: %+v %v", next, e)
	}
	list, e := app.ListRequests(ctx, owner, workflows.Pending, workflows.Page{Size: 1})
	if e != nil || len(list.Items) != 1 || list.Items[0].ID != next.ID || list.PageInfo.TotalItems != 2 || list.PageInfo.TotalPages != 2 {
		t.Fatalf("page: %+v %v", list, e)
	}
	list, e = app.ListRequests(ctx, owner, "", workflows.Page{Number: 2147483647, Size: 100})
	if e != nil || len(list.Items) != 0 || list.PageInfo.TotalItems != 2 {
		t.Fatalf("far page: %+v %v", list, e)
	}
}

func TestPermissionsAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	now := time.Now().UTC()
	app := workflows.New(repo, func() time.Time { return now })
	for _, c := range []workflows.Caller{{}, owner, {ID: "suspended", Role: "suspended_user"}, {ID: "super", Role: "super_admin"}, admin} {
		_, e := app.CreateDisablement(ctx, c, workflows.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Duration(len(repo.disablements)+1) * time.Hour)), EndsAt: ptr(now.Add(time.Duration(len(repo.disablements)+2) * time.Hour)), Reason: "maintenance", Key: uuid.NewString()})
		switch {
		case c.ID == "":
			expectError(t, e, workflows.ErrUnauthenticated)
		case !c.IsAdmin():
			expectError(t, e, workflows.ErrPermissionDenied)
		case e != nil:
			t.Fatal(e)
		}
		_, e = app.SubmitRequest(ctx, c, workflows.SubmitRequest{Proposal: validProposal(), Key: uuid.NewString()})
		switch {
		case c.ID == "":
			expectError(t, e, workflows.ErrUnauthenticated)
		case c.Role == "suspended_user":
			expectError(t, e, workflows.ErrPermissionDenied)
		case e != nil:
			t.Fatal(e)
		}
	}
	for _, modify := range []func(*workflows.Proposal){func(p *workflows.Proposal) { p.Name = " " }, func(p *workflows.Proposal) { p.Name = strings.Repeat("x", 201) }, func(p *workflows.Proposal) { p.Latitude = 91 }, func(p *workflows.Proposal) { p.Longitude = math.NaN() }, func(p *workflows.Proposal) { p.Floor = ptr(strings.Repeat("x", 51)) }, func(p *workflows.Proposal) { p.Contact = ptr(strings.Repeat("x", 501)) }, func(p *workflows.Proposal) { p.Details = strings.Repeat("x", 2001) }, func(p *workflows.Proposal) { p.OpenTo = nil }, func(p *workflows.Proposal) { p.OpenTo = p.OpenFrom }, func(p *workflows.Proposal) { p.BuildingID = "bad" }, func(p *workflows.Proposal) { p.CategoryIDs = []string{categoryID, categoryID} }} {
		p := validProposal()
		modify(&p)
		_, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: p, Key: uuid.NewString()})
		expectError(t, e, workflows.ErrInvalidArgument)
	}
	p := validProposal()
	p.CategoryIDs = nil
	_, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: p, Key: uuid.NewString()})
	expectError(t, e, workflows.ErrFailedPrecondition)
	p = validProposal()
	p.BuildingID = uuid.NewString()
	_, e = app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: p, Key: uuid.NewString()})
	expectError(t, e, workflows.ErrFailedPrecondition)
	_, e = app.ListRequests(ctx, owner, "bogus", workflows.Page{})
	expectError(t, e, workflows.ErrInvalidArgument)
	for _, p := range []workflows.Page{{Number: -1}, {Size: 101}, {Size: -1}} {
		_, e = app.ListRequests(ctx, owner, "", p)
		expectError(t, e, workflows.ErrInvalidArgument)
	}
	repo.locations[locationID] = workflows.Location{ID: locationID, ArchivedAt: &now}
	_, e = app.CreateDisablement(ctx, admin, workflows.CreateDisablement{LocationID: locationID, Reason: "closure", Key: uuid.NewString()})
	expectError(t, e, workflows.ErrFailedPrecondition)
}

func TestRequestPatchClearsFieldsAndClassificationAtomically(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := workflows.New(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	for _, paths := range [][]string{nil, {"id"}, {"name", "name"}, {"open_from"}} {
		_, e = app.UpdateRequest(ctx, owner, workflows.UpdateRequest{ID: r.ID, Paths: paths, ExpectedRevision: 1})
		expectError(t, e, workflows.ErrInvalidArgument)
	}
	v, e := app.UpdateRequest(ctx, admin, workflows.UpdateRequest{ID: r.ID, Proposal: workflows.Proposal{IsSupplier: false}, Paths: []string{"is_supplier", "floor", "contact", "details", "open_from", "open_to"}, ExpectedRevision: 1})
	if e != nil || v.Proposal.Floor != nil || v.Proposal.Contact != nil || v.Proposal.Details != "" || v.Proposal.OpenFrom != nil || v.Proposal.IsSupplier || len(v.Proposal.CategoryIDs) != 0 {
		t.Fatalf("clear: %+v %v", v, e)
	}
}

func TestConcurrentApprovalAndWithdrawalHaveOneTerminalResult(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := workflows.New(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	go func() { _, e := app.ApproveRequest(ctx, admin, r.ID); results <- e }()
	go func() { _, e := app.WithdrawRequest(ctx, owner, r.ID); results <- e }()
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("expected one winner: %v %v", a, b)
	}
	if a != nil {
		expectError(t, a, workflows.ErrFailedPrecondition)
	}
	if b != nil {
		expectError(t, b, workflows.ErrFailedPrecondition)
	}
}
func validProposal() workflows.Proposal {
	return workflows.Proposal{Name: " Cafe ", IsSupplier: true, CategoryIDs: []string{categoryID}, BuildingID: buildingID, Floor: ptr(" B1 "), Latitude: 1.294, Longitude: 103.774, OpenFrom: ptr(int64(22 * 3600000000)), OpenTo: ptr(int64(2 * 3600000000)), Contact: ptr(" contact "), Details: " directions "}
}
func expectError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestScheduledUpdateMayStartNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	app := workflows.New(repo, func() time.Time { return now })
	d, e := app.CreateDisablement(ctx, admin, workflows.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Hour)), Reason: "maintenance", Key: key})
	if e != nil {
		t.Fatal(e)
	}
	updated, e := app.UpdateDisablement(ctx, admin, workflows.UpdateDisablement{ID: d.ID, StartsAt: &now, Paths: []string{"starts_at"}, ExpectedRevision: 1})
	if e != nil || updated.State(now) != workflows.Active {
		t.Fatalf("start now: %+v %v", updated, e)
	}
}

func TestApprovalRevalidatesLegacySupplierProposal(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := workflows.New(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	// A row preserved from the old incomplete-proposal schema.
	legacy := repo.requests[r.ID]
	legacy.Proposal.CategoryIDs = nil
	repo.requests[r.ID] = legacy
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
}

func TestLegacyProposalCoordinatesCanBeRepairedBeforeApproval(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := workflows.New(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, workflows.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	legacy := repo.requests[r.ID]
	legacy.Proposal.CoordinatesMissing = true
	repo.requests[r.ID] = legacy
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, workflows.ErrFailedPrecondition)
	repaired, e := app.UpdateRequest(ctx, owner, workflows.UpdateRequest{ID: r.ID, Proposal: workflows.Proposal{Latitude: 1.294, Longitude: 103.774}, Paths: []string{"coordinates"}, ExpectedRevision: 1})
	if e != nil || repaired.Proposal.CoordinatesMissing {
		t.Fatalf("repair: %+v %v", repaired, e)
	}
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	if e != nil {
		t.Fatal(e)
	}
}

func TestIdempotencyUUIDSpellingsUseOneScope(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	repo.locations[locationID] = workflows.Location{ID: locationID}
	app := workflows.New(repo, time.Now)
	in := workflows.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key}
	first, e := app.CreateDisablement(ctx, admin, in)
	if e != nil {
		t.Fatal(e)
	}
	in.Key = strings.ToUpper(key)
	retry, e := app.CreateDisablement(ctx, admin, in)
	if e != nil || retry.ID != first.ID {
		t.Fatalf("UUID-key retry: %+v %v", retry, e)
	}
	submit := workflows.SubmitRequest{Proposal: validProposal(), Key: key}
	r, e := app.SubmitRequest(ctx, owner, submit)
	if e != nil {
		t.Fatal(e)
	}
	submit.Key = strings.ToUpper(key)
	replayed, e := app.SubmitRequest(ctx, owner, submit)
	if e != nil || replayed.ID != r.ID {
		t.Fatalf("submission UUID-key retry: %+v %v", replayed, e)
	}
}
