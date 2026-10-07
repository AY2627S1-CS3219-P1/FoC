package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/go-chi/chi/v5"
)

type fakePinger struct{ err error }

func (p fakePinger) PingContext(context.Context) error { return p.err }

func newClient(t *testing.T, db Pinger) userv1connect.HealthServiceClient {
	t.Helper()
	router := chi.NewRouter()
	router.Mount(userv1connect.NewHealthServiceHandler(&Handler{DB: db}))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return userv1connect.NewHealthServiceClient(server.Client(), server.URL)
}

func TestHealthServiceDatabaseDown(t *testing.T) {
	_, err := newClient(t, fakePinger{err: errors.New("connection refused")}).Check(
		context.Background(),
		connect.NewRequest(&userv1.CheckRequest{}),
	)
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("expected code %v, got %v (err %v)", connect.CodeUnavailable, code, err)
	}
}
