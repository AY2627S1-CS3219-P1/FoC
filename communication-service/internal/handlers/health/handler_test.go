package health

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
)

type failingDatabase struct{}

func (failingDatabase) Ping(context.Context) error { return errors.New("offline") }

func TestDatabaseUnavailableDoesNotReportReady(t *testing.T) {
	h := &Handler{DB: failingDatabase{}}
	_, err := h.Check(t.Context(), connect.NewRequest(&communicationv1.CheckRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestDisabledTelegramDoesNotFailHealth(t *testing.T) {
	response, err := (&Handler{}).Check(t.Context(), connect.NewRequest(&communicationv1.CheckRequest{}))
	if err != nil || response.Msg.Status != "ok" || response.Msg.TelegramEnabled {
		t.Fatalf("health=%v err=%v", response, err)
	}
}
