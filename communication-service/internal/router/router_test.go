package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

// fakeAuthenticate stands in for httpauth: a request carrying X-Test-User is
// treated as authenticated as that user.
func fakeAuthenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-User"); id != "" {
			r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: id}))
		}
		next.ServeHTTP(w, r)
	})
}

type fakeLinkService struct {
	communicationv1connect.UnimplementedTelegramLinkServiceHandler
}

func (fakeLinkService) GetTelegramLink(
	context.Context, *connect.Request[communicationv1.GetTelegramLinkRequest],
) (*connect.Response[communicationv1.GetTelegramLinkResponse], error) {
	return connect.NewResponse(&communicationv1.GetTelegramLinkResponse{Linked: true}), nil
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(Services{
		Health:       &health.Handler{TelegramEnabled: func() bool { return true }},
		TelegramLink: fakeLinkService{},
	}, fakeAuthenticate))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthIsPublic(t *testing.T) {
	srv := newServer(t)
	client := communicationv1connect.NewHealthServiceClient(srv.Client(), srv.URL)

	resp, err := client.Check(context.Background(), connect.NewRequest(&communicationv1.CheckRequest{}))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Msg.GetStatus() != "ok" || !resp.Msg.GetTelegramEnabled() {
		t.Fatalf("Check = %+v", resp.Msg)
	}
}

func TestTelegramLinkRequiresCaller(t *testing.T) {
	srv := newServer(t)
	client := communicationv1connect.NewTelegramLinkServiceClient(srv.Client(), srv.URL)

	_, err := client.GetTelegramLink(
		context.Background(),
		connect.NewRequest(&communicationv1.GetTelegramLinkRequest{}),
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("without caller: err = %v, want unauthenticated", err)
	}

	req := connect.NewRequest(&communicationv1.GetTelegramLinkRequest{})
	req.Header().Set("X-Test-User", "4f6c1a2e-0d8b-4b8e-9b77-3f2a1c5d6e7f")
	resp, err := client.GetTelegramLink(context.Background(), req)
	if err != nil || !resp.Msg.GetLinked() {
		t.Fatalf("with caller: %+v, %v", resp, err)
	}
}
