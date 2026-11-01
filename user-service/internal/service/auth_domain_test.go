package service

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func TestAuthenticationDomainPolicy(t *testing.T) {
	lookupFailure := errors.New("domain database unavailable")
	for _, operation := range []string{"request login link", "login", "refresh", "request registration link", "register"} {
		roles := models.AllRoles
		if operation == "register" || operation == "request registration link" {
			roles = []models.RoleName{models.RoleUser}
		}
		for _, role := range roles {
			for _, result := range []string{"allowed", "disallowed", "lookup failure"} {
				t.Run(operation+"/"+string(role)+"/"+result, func(t *testing.T) {
					now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
					store := newFakeStore()
					svc := setupService(t, store, &now, true, nil)
					ctx := context.Background()
					var attempt func() error
					switch operation {
					case "request login link":
						user := testModelUser("user@example.com", role)
						store.users[user.ID] = user
						attempt = func() error { return svc.RequestLink(ctx, " USER@EXAMPLE.COM ") }
					case "request registration link":
						attempt = func() error { return svc.RequestLink(ctx, " NEW@EXAMPLE.COM ") }
					case "register":
						token := addRegistrationChallenge(t, store, now)
						attempt = func() error {
							_, _, err := svc.Register(
								ctx,
								token,
								jwt.Profile{DisplayName: "New", PhoneNumber: "+6591234567"},
							)
							return err
						}
					case "login", "refresh":
						token := addLoginChallenge(t, store, now)
						if operation == "refresh" {
							_, tokens, err := svc.Login(ctx, token)
							if err != nil {
								t.Fatal(err)
							}
							token = tokens.RefreshToken
						}
						// Use the persisted role, including changes made since token issuance.
						for id, user := range store.users {
							user.Role = role
							store.users[id] = user
						}
						attempt = func() error {
							if operation == "login" {
								_, _, err := svc.Login(ctx, token)
								return err
							}
							_, _, err := svc.Refresh(ctx, token)
							return err
						}
					}
					if result == "disallowed" {
						delete(store.allowedDomains, "example.com")
					} else if result == "lookup failure" {
						store.domainErr = lookupFailure
					}
					users := cloneMap(store.users)
					logins := cloneMap(store.logins)
					registrations := cloneMap(store.registrations)
					sessions := cloneMap(store.sessions)
					sender := svc.deps.EmailSender.(*mockEmailSender)
					messages := len(sender.messages)
					err := attempt()
					if result == "allowed" || role == models.RoleSuperAdmin {
						if err != nil {
							t.Fatalf("permitted authentication failed: %v", err)
						}
						return
					}
					var external api.ExternalError
					if result == "disallowed" {
						if !errors.Is(err, ErrEmailDomainNotAllowed) || !errors.As(err, &external) ||
							external.Code() != http.StatusForbidden {
							t.Fatalf("expected shared Forbidden domain error, got %v", err)
						}
					} else if !errors.Is(err, lookupFailure) || errors.As(err, &external) {
						t.Fatalf("lookup failure was lost or misclassified: %v", err)
					}
					if !reflect.DeepEqual(store.users, users) || !reflect.DeepEqual(store.logins, logins) ||
						!reflect.DeepEqual(
							store.registrations,
							registrations,
						) || !reflect.DeepEqual(store.sessions, sessions) {
						t.Fatal("failed domain check changed users, challenges, or sessions")
					}
					if len(sender.messages) != messages {
						t.Fatal("failed domain check sent an email")
					}
				})
			}
		}
	}
}

func TestDomainRemovalBlocksLoginAndRefreshUntilAllowedAgain(t *testing.T) {
	for _, operation := range []string{"login", "refresh"} {
		t.Run(operation, func(t *testing.T) {
			now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
			store := newFakeStore()
			svc := setupService(t, store, &now, true, nil)
			ctx := context.Background()
			token := addLoginChallenge(t, store, now)
			if operation == "refresh" {
				_, tokens, err := svc.Login(ctx, token)
				if err != nil {
					t.Fatal(err)
				}
				token = tokens.RefreshToken
			}
			attempt := func() error {
				if operation == "login" {
					_, _, err := svc.Login(ctx, token)
					return err
				}
				_, _, err := svc.Refresh(ctx, token)
				return err
			}
			delete(store.allowedDomains, "example.com")
			if err := attempt(); !errors.Is(err, ErrEmailDomainNotAllowed) {
				t.Fatalf("domain removal did not block authentication: %v", err)
			}
			store.allowedDomains["example.com"] = struct{}{}
			if err := attempt(); err != nil {
				t.Fatalf("domain rejection consumed the challenge or refresh session: %v", err)
			}
		})
	}
}
