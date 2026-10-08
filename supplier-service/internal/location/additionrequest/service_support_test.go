package additionrequest_test

import (
	"errors"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	"testing"
	"time"
)

func newTestService(repo additionrequest.Repository, clock func() time.Time) *additionrequest.Service {
	return additionrequest.NewService(repo, clock)
}

func expectError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func validProposal() additionrequest.Proposal {
	return additionrequest.Proposal{Name: " Cafe ", IsSupplier: true, CategoryIDs: []string{categoryID}, BuildingID: buildingID, Floor: ptr(" B1 "), Latitude: 1.294, Longitude: 103.774, OpenFrom: ptr(int64(22 * 3600000000)), OpenTo: ptr(int64(2 * 3600000000)), Contact: ptr(" contact "), Details: " directions "}
}
