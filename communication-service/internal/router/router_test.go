package router

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/health"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

func TestHealthIsMounted(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		srv := httptest.NewServer(New(Services{Health: &health.Handler{TelegramEnabled: func() bool { return enabled }}}))
		client := communicationv1connect.NewHealthServiceClient(srv.Client(), srv.URL)

		resp, err := client.Check(context.Background(), connect.NewRequest(&communicationv1.CheckRequest{}))
		srv.Close()
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if resp.Msg.GetStatus() != "ok" || resp.Msg.GetTelegramEnabled() != enabled {
			t.Fatalf("Check = %+v, want ok and telegram_enabled=%v", resp.Msg, enabled)
		}
	}
}
