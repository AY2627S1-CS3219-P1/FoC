package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

// deletionLogic embeds Logic so only the method under test needs a body.
type deletionLogic struct {
	Logic
	result    service.DeleteMyAccountResult
	err       error
	gotCookie string
}

func (l *deletionLogic) DeleteMyAccount(_ context.Context, refreshToken string) (service.DeleteMyAccountResult, error) {
	l.gotCookie = refreshToken
	return l.result, l.err
}

func TestDeleteMyAccountReplacesRefreshCookie(t *testing.T) {
	now := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	user := models.User{ID: uuid.New(), Email: "person@example.com", DisplayName: "Person",
		Role: models.RoleUser, IsDeleting: true}
	logic := &deletionLogic{result: service.DeleteMyAccountResult{
		RequestedAt: now,
		User:        user,
		Tokens: jwt.AuthTokens{
			AccessToken: "access-token", RefreshToken: "replacement-refresh",
			AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(24 * time.Hour),
		},
	}}
	handler := &Handler{Logic: logic}
	request := connect.NewRequest(&userv1.DeleteMyAccountRequest{})
	request.Header().Set("Cookie", (&http.Cookie{Name: RefreshCookieName, Value: "old-refresh"}).String())

	response, err := handler.DeleteMyAccount(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if logic.gotCookie != "old-refresh" {
		t.Fatalf("handler read cookie %q", logic.gotCookie)
	}
	if response.Msg.User == nil || !response.Msg.User.IsDeleting || response.Msg.AccessToken != "access-token" {
		t.Fatalf("response = %+v", response.Msg)
	}
	if !response.Msg.RequestedAt.AsTime().Equal(now) {
		t.Fatalf("requested_at = %v", response.Msg.RequestedAt)
	}
	cookies := (&http.Response{Header: response.Header()}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != RefreshCookieName ||
		cookies[0].Value != "replacement-refresh" || cookies[0].MaxAge <= 0 {
		t.Fatalf("refresh cookie was not replaced: %+v", cookies)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("deletion response is cacheable")
	}
}
