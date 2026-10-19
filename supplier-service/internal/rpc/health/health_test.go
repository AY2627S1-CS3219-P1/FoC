package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
)

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReadinessChecksDatabase(t *testing.T) {
	for _, down := range []bool{false, true} {
		called := false
		db := pingFunc(func(ctx context.Context) error {
			called = true
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 2*time.Second {
				t.Fatal("database ping is not bounded")
			}
			if down {
				return errors.New("private database error")
			}
			return nil
		})
		response, err := NewServer(db).Check(context.Background(), connect.NewRequest(&supplierv1.CheckRequest{}))
		if !called {
			t.Fatal("database not checked")
		}
		if down {
			if connect.CodeOf(err) != connect.CodeUnavailable || err.Error() != "unavailable: database unavailable" {
				t.Fatalf("unexpected error: %v", err)
			}
		} else if err != nil || response.Msg.Status != "ok" {
			t.Fatalf("response=%v err=%v", response, err)
		}
	}
}

func TestMissingDatabaseIsNotReady(t *testing.T) {
	_, err := NewServer(nil).Check(context.Background(), connect.NewRequest(&supplierv1.CheckRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err=%v", err)
	}
}
