package disablement_test

import (
	"errors"
	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	"testing"
	"time"
)

func newTestService(repo disablement.Repository, clock func() time.Time) *disablement.Service {
	return disablement.NewService(repo, clock)
}

func expectError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
