package health

import (
	"connectrpc.com/connect"
	"context"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"testing"
)

func TestOptionalTelegramDoesNotFailHealth(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		handler := &Handler{TelegramEnabled: func() bool { return enabled }}
		response, err := handler.Check(context.Background(), connect.NewRequest(&communicationv1.CheckRequest{}))
		if err != nil || response.Msg.Status != "ok" || response.Msg.TelegramEnabled != enabled {
			t.Fatalf("response=%v err=%v", response, err)
		}
	}
}
