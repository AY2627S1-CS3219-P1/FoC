package location_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	"github.com/google/uuid"
)

func TestPermissionsAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	repo.locations[locationID] = additionrequest.Location{ID: locationID}
	now := time.Now().UTC()
	app := newTestService(repo, func() time.Time { return now })
	for _, c := range []additionrequest.Caller{{}, owner, {ID: "suspended", Role: "suspended_user"}, {ID: "super", Role: "super_admin"}, admin} {
		_, e := app.CreateDisablement(ctx, c, disablement.CreateDisablement{LocationID: locationID, StartsAt: ptr(now.Add(time.Duration(len(repo.disablements)+1) * time.Hour)), EndsAt: ptr(now.Add(time.Duration(len(repo.disablements)+2) * time.Hour)), Reason: "maintenance", Key: uuid.NewString()})
		switch {
		case c.ID == "":
			expectError(t, e, additionrequest.ErrUnauthenticated)
		case !c.IsAdmin():
			expectError(t, e, additionrequest.ErrPermissionDenied)
		case e != nil:
			t.Fatal(e)
		}
		_, e = app.SubmitRequest(ctx, c, additionrequest.SubmitRequest{Proposal: validProposal(), Key: uuid.NewString()})
		switch {
		case c.ID == "":
			expectError(t, e, additionrequest.ErrUnauthenticated)
		case c.Role == "suspended_user":
			expectError(t, e, additionrequest.ErrPermissionDenied)
		case e != nil:
			t.Fatal(e)
		}
	}
	for _, modify := range []func(*additionrequest.Proposal){func(p *additionrequest.Proposal) { p.Name = " " }, func(p *additionrequest.Proposal) { p.Name = strings.Repeat("x", 201) }, func(p *additionrequest.Proposal) { p.Latitude = 91 }, func(p *additionrequest.Proposal) { p.Longitude = math.NaN() }, func(p *additionrequest.Proposal) { p.Floor = ptr(strings.Repeat("x", 51)) }, func(p *additionrequest.Proposal) { p.Contact = ptr(strings.Repeat("x", 501)) }, func(p *additionrequest.Proposal) { p.Details = strings.Repeat("x", 2001) }, func(p *additionrequest.Proposal) { p.OpenTo = nil }, func(p *additionrequest.Proposal) { p.OpenTo = p.OpenFrom }, func(p *additionrequest.Proposal) { p.BuildingID = "bad" }, func(p *additionrequest.Proposal) { p.CategoryIDs = []string{categoryID, categoryID} }} {
		p := validProposal()
		modify(&p)
		_, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: p, Key: uuid.NewString()})
		expectError(t, e, additionrequest.ErrInvalidArgument)
	}
	p := validProposal()
	p.CategoryIDs = nil
	_, e := app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: p, Key: uuid.NewString()})
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	p = validProposal()
	p.BuildingID = uuid.NewString()
	_, e = app.SubmitRequest(ctx, owner, additionrequest.SubmitRequest{Proposal: p, Key: uuid.NewString()})
	expectError(t, e, additionrequest.ErrFailedPrecondition)
	_, e = app.ListRequests(ctx, owner, "bogus", additionrequest.Page{})
	expectError(t, e, additionrequest.ErrInvalidArgument)
	for _, p := range []additionrequest.Page{{Number: -1}, {Size: 101}, {Size: -1}} {
		_, e = app.ListRequests(ctx, owner, "", p)
		expectError(t, e, additionrequest.ErrInvalidArgument)
	}
	repo.locations[locationID] = additionrequest.Location{ID: locationID, ArchivedAt: &now}
	_, e = app.CreateDisablement(ctx, admin, disablement.CreateDisablement{LocationID: locationID, Reason: "closure", Key: uuid.NewString()})
	expectError(t, e, additionrequest.ErrFailedPrecondition)
}

func validProposal() additionrequest.Proposal {
	return additionrequest.Proposal{Name: " Cafe ", IsSupplier: true, CategoryIDs: []string{categoryID}, BuildingID: buildingID, Floor: ptr(" B1 "), Latitude: 1.294, Longitude: 103.774, OpenFrom: ptr(int64(22 * 3600000000)), OpenTo: ptr(int64(2 * 3600000000)), Contact: ptr(" contact "), Details: " directions "}
}

func expectError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
