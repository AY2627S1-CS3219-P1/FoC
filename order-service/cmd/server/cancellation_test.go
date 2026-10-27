package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

type cancellationStartupFixture struct {
	order.CancellationRepository
	pending bool
}

func (r cancellationStartupFixture) HasPendingCancellations(context.Context) (bool, error) {
	return r.pending, nil
}

// Source: the approved durable recovery requirement: disabling new admissions
// must not strand existing adjustment/revert intent. Reachability is not startup.
func TestCancellationStartupPreservesRecoveryDuringOutage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	file := filepath.Join(t.TempDir(), "workload-token")
	if err := os.WriteFile(file, []byte("fixture-workload-token"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		pending        bool
		config         config
		valid, recover bool
	}{
		{"no pending financial intent", false, config{}, true, false},
		{"cannot abandon pending intent", true, config{}, false, false},
		{"recovery while dependency down", true, config{CreditServiceURL: server.URL, CreditTokenFile: file}, true, true},
		{"unreadable identity", true, config{CreditServiceURL: server.URL, CreditTokenFile: file + "-missing"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assigner, recover, err := newCanceller(context.Background(), cancellationStartupFixture{pending: tc.pending}, tc.config)
			if (err == nil) != tc.valid || recover != tc.recover || (tc.valid && assigner == nil) {
				t.Fatalf("assigner=%v recover=%v err=%v", assigner, recover, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("startup performed remote financial calls")
	}
}
