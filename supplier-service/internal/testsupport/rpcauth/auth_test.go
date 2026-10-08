package rpcauth

import (
	"connectrpc.com/connect"
	"context"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBearerRefreshesTokenForEachRequest(t *testing.T) {
	auth := New(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	tokens := make(chan string, 2)
	_, handler := userv1connect.NewPublicKeyServiceHandler(publicKeyService{key: &auth.key.PublicKey})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens <- r.Header.Get("Authorization")[len("Bearer "):]
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := userv1connect.NewPublicKeyServiceClient(server.Client(), server.URL, auth.Bearer(t, "admin"))
	for i := range 2 {
		if i > 0 {
			now = now.Add(10 * time.Minute)
		}
		if _, err := client.GetPublicKeys(context.Background(), connect.NewRequest(&userv1.GetPublicKeysRequest{})); err != nil {
			t.Fatal(err)
		}
		token, err := jwt.Parse(<-tokens, func(*jwt.Token) (any, error) { return &auth.key.PublicKey, nil }, jwt.WithoutClaimsValidation(), jwt.WithValidMethods([]string{"ES256"}))
		if err != nil {
			t.Fatal(err)
		}
		expiry, err := token.Claims.GetExpirationTime()
		if err != nil || expiry == nil || !expiry.Time.Equal(now.Add(time.Minute)) {
			t.Fatalf("request %d reused an expired token: expiry=%v error=%v", i, expiry, err)
		}
		if token.Claims.(jwt.MapClaims)["role"] != "admin" {
			t.Fatal("caller role changed")
		}
	}
}
