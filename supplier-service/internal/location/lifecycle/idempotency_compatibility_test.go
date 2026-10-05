package lifecycle_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
)

func TestHistoricalWorkflowHashesAndCanonicalScopes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = w.Location{ID: locationID}
	app := w.New(repo, func() time.Time { return now })
	// These fixed SHA-256 values encode the historical untagged JSON fields,
	// including omitted start, normalized proposal strings and microsecond hours.
	disablementScope := idempotency.Scope{Caller: admin.ID, Method: "CreateDisablement", Key: key}
	requestScope := idempotency.Scope{Caller: owner.ID, Method: "SubmitLocationAdditionRequest", Key: key}
	first, err := app.CreateDisablement(ctx, admin, w.CreateDisablement{LocationID: strings.ToUpper(locationID), Reason: " closure ", Key: "urn:uuid:" + key})
	if err != nil {
		t.Fatal(err)
	}
	request, err := app.SubmitRequest(ctx, owner, w.SubmitRequest{Proposal: validProposal(), Key: "{" + strings.ToUpper(key) + "}"})
	if err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[idempotency.Scope]string{
		disablementScope: "a467c12437bffbd0e36a66b03f086f845071be9a53e2ccd1496880883ebc0de2",
		requestScope:     "fc0b3c2c608c5e0cf9b6c39f21319d76540d9a85cd385c09f8805a8fc30dcefd",
	} {
		record, ok := repo.keys[scope]
		if !ok || record.Hash != want || !record.ExpiresAt.Equal(now.Add(24*time.Hour)) {
			t.Fatalf("historical scope %+v: %+v, want hash %s", scope, record, want)
		}
	}
	now = now.Add(time.Hour)
	retried, err := app.CreateDisablement(ctx, admin, w.CreateDisablement{LocationID: locationID, Reason: "closure", Key: strings.ReplaceAll(key, "-", "")})
	if err != nil || retried.ID != first.ID || !retried.StartsAt.Equal(first.StartsAt) {
		t.Fatalf("disablement retry: %+v %v", retried, err)
	}
	proposal := validProposal()
	proposal.BuildingID = "urn:uuid:" + strings.ToUpper(buildingID)
	proposal.CategoryIDs = []string{"{" + strings.ToUpper(categoryID) + "}"}
	retriedRequest, err := app.SubmitRequest(ctx, owner, w.SubmitRequest{Proposal: proposal, Key: strings.ReplaceAll(key, "-", "")})
	if err != nil || retriedRequest.ID != request.ID || len(repo.keys) != 2 {
		t.Fatalf("proposal retry: %+v %v, keys=%d", retriedRequest, err, len(repo.keys))
	}
	proposal.Details = "changed"
	_, err = app.SubmitRequest(ctx, owner, w.SubmitRequest{Proposal: proposal, Key: key})
	if !errors.Is(err, w.ErrAlreadyExists) {
		t.Fatalf("changed hash error: %v", err)
	}
}

func TestWorkflowCreationTimeAfterRetryLock(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	lockedAt := now.Add(2 * time.Hour)
	repo := newTestRepository()
	repo.locations[locationID] = w.Location{ID: locationID}
	repo.onRetryLock = func() { now = lockedAt }
	app := w.New(repo, func() time.Time { return now })
	got, err := app.CreateDisablement(context.Background(), admin, w.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key})
	if err != nil || !got.StartsAt.Equal(lockedAt) || !got.CreatedAt.Equal(lockedAt) {
		t.Fatalf("callback timestamp: %+v %v", got, err)
	}
	record := repo.keys[idempotency.Scope{Caller: admin.ID, Method: "CreateDisablement", Key: key}]
	if !record.ExpiresAt.Equal(lockedAt.Add(24 * time.Hour)) {
		t.Fatalf("expiry %v", record.ExpiresAt)
	}
}

func TestWorkflowRetrySaveFailureRollsBackResource(t *testing.T) {
	repo := newTestRepository()
	repo.locations[locationID] = w.Location{ID: locationID}
	repo.failRetrySave = true
	app := w.New(repo, time.Now)
	in := w.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key}
	_, err := app.CreateDisablement(context.Background(), admin, in)
	if !errors.Is(err, w.ErrFailedPrecondition) || len(repo.disablements) != 0 || len(repo.keys) != 0 {
		t.Fatalf("save rollback error=%v resources=%d keys=%d", err, len(repo.disablements), len(repo.keys))
	}
	repo.failRetrySave = false
	if _, err := app.CreateDisablement(context.Background(), admin, in); err != nil || len(repo.disablements) != 1 || len(repo.keys) != 1 {
		t.Fatalf("retry error=%v resources=%d keys=%d", err, len(repo.disablements), len(repo.keys))
	}
}

func TestWorkflowInvalidRetryKeyDoesNotWrite(t *testing.T) {
	for _, key := range []string{"", "not-a-uuid"} {
		t.Run(key, func(t *testing.T) {
			repo := newTestRepository()
			repo.locations[locationID] = w.Location{ID: locationID}
			app := w.New(repo, time.Now)
			_, disablementErr := app.CreateDisablement(context.Background(), admin, w.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key})
			_, requestErr := app.SubmitRequest(context.Background(), owner, w.SubmitRequest{Proposal: validProposal(), Key: key})
			if !errors.Is(disablementErr, w.ErrInvalidArgument) || !errors.Is(requestErr, w.ErrInvalidArgument) {
				t.Fatalf("invalid-key translation: disablement=%v request=%v", disablementErr, requestErr)
			}
			if len(repo.disablements) != 0 || len(repo.requests) != 0 || len(repo.keys) != 0 {
				t.Fatalf("invalid key wrote resources: disablements=%d requests=%d keys=%d", len(repo.disablements), len(repo.requests), len(repo.keys))
			}
		})
	}
}
