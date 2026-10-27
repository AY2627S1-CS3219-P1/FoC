package health

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

type databaseFunc func(context.Context) error

func (f databaseFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestHealthProbesDatabaseAndHidesDiagnostics(t *testing.T) {
	called := false
	server := &Server{DB: databaseFunc(func(ctx context.Context) error {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("health database probe lacks timeout")
		}
		return errors.New("sensitive dependency diagnostics")
	})}
	_, err := server.Check(context.Background(), connect.NewRequest(&orderv1.CheckRequest{}))
	if !called || connect.CodeOf(err) != connect.CodeInternal || err.Error() != "internal: internal error" {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestHealthPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{DB: databaseFunc(func(probe context.Context) error {
		if !errors.Is(probe.Err(), context.Canceled) {
			t.Fatal("caller cancellation lost")
		}
		return probe.Err()
	})}
	if _, err := server.Check(ctx, connect.NewRequest(&orderv1.CheckRequest{})); err == nil {
		t.Fatal("cancelled health succeeded")
	}
}
