package location_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

func TestHistoricalWorkflowHashesAndCanonicalScopes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	repo := newTestRepository()
	repo.locations[locationID] = additionrequest.Location{ID: locationID}
	app := newTestService(repo, func() time.Time { return now })
	// These fixed SHA-256 values encode the historical untagged JSON fields,
	// including omitted start, normalized proposal strings and microsecond hours.
	disablementScope := idempotency.Scope{Caller: admin.ID, Method: "CreateDisablement", Key: key}
	requestScope := idempotency.Scope{Caller: owner.ID, Method: "SubmitLocationAdditionRequest", Key: key}
	first, err := app.CreateDisablement(ctx, admin, disablement.CreateDisablement{LocationID: strings.ToUpper(locationID), Reason: " closure ", Key: "urn:uuid:" + key})
	if err != nil {
		t.Fatal(err)
	}
	request, err := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: "{" + strings.ToUpper(key) + "}"})
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
	retried, err := app.CreateDisablement(ctx, admin, disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: strings.ReplaceAll(key, "-", "")})
	if err != nil || retried.ID != first.ID || !retried.StartsAt.Equal(first.StartsAt) {
		t.Fatalf("disablement retry: %+v %v", retried, err)
	}
	proposal := validProposal()
	proposal.BuildingID = "urn:uuid:" + strings.ToUpper(buildingID)
	proposal.CategoryIDs = []string{"{" + strings.ToUpper(categoryID) + "}"}
	retriedRequest, err := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: proposal, Key: strings.ReplaceAll(key, "-", "")})
	if err != nil || retriedRequest.ID != request.ID || len(repo.keys) != 2 {
		t.Fatalf("proposal retry: %+v %v, keys=%d", retriedRequest, err, len(repo.keys))
	}
	proposal.Details = "changed"
	_, err = app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: proposal, Key: key})
	if !errors.Is(err, additionrequest.ErrAlreadyExists) {
		t.Fatalf("changed hash error: %v", err)
	}
}

func TestWorkflowInvalidRetryKeyDoesNotWrite(t *testing.T) {
	for _, key := range []string{"", "not-a-uuid"} {
		t.Run(key, func(t *testing.T) {
			repo := newTestRepository()
			repo.locations[locationID] = additionrequest.Location{ID: locationID}
			app := newTestService(repo, time.Now)
			_, disablementErr := app.CreateDisablement(context.Background(), admin, disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: key})
			_, requestErr := app.SubmitRequest(context.Background(), owner, additionrequest.SubmitRequest{Proposal: validProposal(), Key: key})
			if !errors.Is(disablementErr, additionrequest.ErrInvalidArgument) || !errors.Is(requestErr, additionrequest.ErrInvalidArgument) {
				t.Fatalf("invalid-key translation: disablement=%v request=%v", disablementErr, requestErr)
			}
			if len(repo.disablements) != 0 || len(repo.requests) != 0 || len(repo.keys) != 0 {
				t.Fatalf("invalid key wrote resources: disablements=%d requests=%d keys=%d", len(repo.disablements), len(repo.requests), len(repo.keys))
			}
		})
	}
}
