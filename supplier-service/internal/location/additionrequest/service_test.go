package additionrequest_test

import (
	"context"
	"errors"

	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
)

func TestRequestApprovalVisibilityRedactionAndRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	app := newTestService(repo, func() time.Time { return now })
	in := additionrequest.SubmitRequest{Proposal: validProposal(), Key: key}
	r, e := app.SubmitRequest(ctx, owner, in)
	if e != nil {
		t.Fatal(e)
	}
	other := additionrequest.Caller{ID: "other", Role: "user"}
	_, e = app.GetRequest(ctx, other, r.ID)
	expectError(t, e, additionrequest.ErrNotFound)
	list, e := app.ListRequests(ctx, other, "", additionrequest.Page{})
	if e != nil || list.PageInfo.TotalItems != 0 {
		t.Fatalf("hidden list: %+v %v", list, e)
	}
	updated, e := app.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: r.ID, Proposal: additionrequest.Proposal{Details: "updated"}, Paths: []string{"details"}, ExpectedRevision: 1})
	if e != nil || updated.Revision != 2 || updated.Proposal.Name != "Cafe" {
		t.Fatalf("patch: %+v %v", updated, e)
	}
	_, e = app.UpdateRequest(ctx, admin, additionrequest.UpdateRequest{ID: r.ID, Proposal: additionrequest.Proposal{Details: "stale"}, Paths: []string{"details"}, ExpectedRevision: 1})
	expectError(t, e, additionrequest.ErrAborted)
	result, e := app.ApproveRequest(ctx, admin, r.ID)
	if e != nil || result.Location.ID == "" || result.Request.Status != additionrequest.Approved || result.Location.Proposal.Details != "updated" {
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
	list, e = app.ListRequests(ctx, other, additionrequest.Approved, additionrequest.Page{})
	if e != nil || list.PageInfo.TotalItems != 1 || list.Items[0].SubmittedBy != "" {
		t.Fatalf("public list: %+v %v", list, e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, "different transition")
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	_, e = app.WithdrawRequest(ctx, owner, r.ID)
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	_, e = app.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: r.ID, Paths: []string{"details"}, ExpectedRevision: 3})
	expectError(t, e, additionrequest.ErrFailedPrecondition)
}

func TestRequestWithdrawRejectAndApprovalRollback(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Now().UTC()
	app := newTestService(repo, func() time.Time { return now })
	r, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	repo.failCreate = true
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	if e == nil {
		t.Fatal("expected dependency failure")
	}
	stored, e := app.GetRequest(ctx, owner, r.ID)
	if e != nil || stored.Status != additionrequest.Pending || stored.Revision != 1 {
		t.Fatalf("rollback: %+v %v", stored, e)
	}
	_, e = app.WithdrawRequest(ctx, admin, r.ID)
	expectError(t, e, additionrequest.ErrPermissionDenied)
	withdrawn, e := app.WithdrawRequest(ctx, owner, r.ID)
	if e != nil || withdrawn.Status != additionrequest.Withdrawn || withdrawn.ReviewedBy != nil {
		t.Fatalf("withdraw: %+v %v", withdrawn, e)
	}
	retry, e := app.WithdrawRequest(ctx, owner, r.ID)
	if e != nil || retry.Revision != withdrawn.Revision {
		t.Fatalf("withdraw retry: %+v %v", retry, e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, "note")
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	r, e = app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: "c3600892-6029-4910-b1c9-d3e892039f63"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = app.RejectRequest(ctx, admin, r.ID, " ")
	expectError(t, e, additionrequest.ErrInvalidArgument)
	rejected, e := app.RejectRequest(ctx, admin, r.ID, " not suitable ")
	if e != nil || rejected.Status != additionrequest.Rejected || *rejected.ReviewNote != "not suitable" {
		t.Fatalf("reject: %+v %v", rejected, e)
	}
	retry, e = app.RejectRequest(ctx, admin, r.ID, "new note")
	if e != nil || *retry.ReviewNote != "not suitable" {
		t.Fatalf("reject retry: %+v %v", retry, e)
	}
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, additionrequest.ErrFailedPrecondition)
}

func TestApprovalRollsBackLocationWhenRequestSaveFails(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	app := newTestService(repo, func() time.Time { return now })
	request, err := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
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
	if stored.Status != additionrequest.Pending || stored.Revision != 1 || stored.ResultingLocationID != nil || stored.ReviewedBy != nil || stored.ReviewedAt != nil || stored.ReviewNote != nil {
		t.Fatalf("rollback changed pending request: %+v", stored)
	}
}

func TestRequestIdempotencyScopesExpiryAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	app := newTestService(repo, func() time.Time { return now })
	in := additionrequest.SubmitRequest{Proposal: validProposal(), Key: key}
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
	expectError(t, e, additionrequest.ErrAlreadyExists)
	other := additionrequest.Caller{ID: "another", Role: "user"}
	_, e = app.SubmitRequest(ctx, other, in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(24 * time.Hour)
	next, e := app.SubmitRequest(ctx, owner, in)
	if e != nil || next.ID == first.ID {
		t.Fatalf("expiry: %+v %v", next, e)
	}
	list, e := app.ListRequests(ctx, owner, additionrequest.Pending, additionrequest.Page{Size: 1})
	if e != nil || len(list.Items) != 1 || list.Items[0].ID != next.ID || list.PageInfo.TotalItems != 2 || list.PageInfo.TotalPages != 2 {
		t.Fatalf("page: %+v %v", list, e)
	}
	list, e = app.ListRequests(ctx, owner, "", additionrequest.Page{Number: 2147483647, Size: 100})
	if e != nil || len(list.Items) != 0 || list.PageInfo.TotalItems != 2 {
		t.Fatalf("far page: %+v %v", list, e)
	}
}

func TestRequestPatchClearsFieldsAndClassificationAtomically(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := newTestService(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	for _, paths := range [][]string{nil, {"id"}, {"name", "name"}, {"open_from"}} {
		_, e = app.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: r.ID, Paths: paths, ExpectedRevision: 1})
		expectError(t, e, additionrequest.ErrInvalidArgument)
	}
	v, e := app.UpdateRequest(ctx, admin, additionrequest.UpdateRequest{ID: r.ID, Proposal: additionrequest.Proposal{IsSupplier: false}, Paths: []string{"is_supplier", "floor", "contact", "details", "open_from", "open_to"}, ExpectedRevision: 1})
	if e != nil || v.Proposal.Floor != nil || v.Proposal.Contact != nil || v.Proposal.Details != "" || v.Proposal.OpenFrom != nil || v.Proposal.IsSupplier || len(v.Proposal.CategoryIDs) != 0 {
		t.Fatalf("clear: %+v %v", v, e)
	}
}

func TestConcurrentApprovalAndWithdrawalHaveOneTerminalResult(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := newTestService(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
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
		expectError(t, a, additionrequest.ErrFailedPrecondition)
	}
	if b != nil {
		expectError(t, b, additionrequest.ErrFailedPrecondition)
	}
}

func TestApprovalRevalidatesLegacySupplierProposal(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := newTestService(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	// A row preserved from the old incomplete-proposal schema.
	legacy := repo.requests[r.ID]
	legacy.Proposal.CategoryIDs = nil
	repo.requests[r.ID] = legacy
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, additionrequest.ErrFailedPrecondition)
}

func TestLegacyProposalCoordinatesCanBeRepairedBeforeApproval(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	app := newTestService(repo, time.Now)
	r, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
	if e != nil {
		t.Fatal(e)
	}
	legacy := repo.requests[r.ID]
	legacy.Proposal.CoordinatesMissing = true
	repo.requests[r.ID] = legacy
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	repaired, e := app.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: r.ID, Proposal: additionrequest.Proposal{Latitude: 1.294, Longitude: 103.774}, Paths: []string{"coordinates"}, ExpectedRevision: 1})
	if e != nil || repaired.Proposal.CoordinatesMissing {
		t.Fatalf("repair: %+v %v", repaired, e)
	}
	_, e = app.ApproveRequest(ctx, admin, r.ID)
	if e != nil {
		t.Fatal(e)
	}
}
