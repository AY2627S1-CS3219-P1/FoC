package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

type creationStartupRepository struct {
	order.CreationRepository
	pending bool
}

func (r creationStartupRepository) HasPendingCreations(context.Context) (bool, error) {
	return r.pending, nil
}

func TestCreationStartupRequiresRecoveryCredentialsButNotReachability(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name           string
		pending        bool
		config         config
		valid, recover bool
	}{
		{"read-only first deployment", false, config{}, true, false},
		{"cannot strand pending workflows", true, config{}, false, false},
		{"admissions disabled but recovery configured", true, config{CreditServiceURL: server.URL}, true, true},
		{"configured outage with no pending work", false, config{CreditServiceURL: server.URL}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creator, recover, err := newCreator(
				context.Background(),
				creationStartupRepository{pending: tc.pending},
				tc.config,
				nil,
			)
			if (err == nil) != tc.valid || recover != tc.recover {
				t.Fatalf("creator=%v recover=%v err=%v", creator, recover, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("startup depended on Credit availability")
	}
}
