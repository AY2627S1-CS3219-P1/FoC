package router_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/google/uuid"
)

type protectedUsers struct{ users map[uuid.UUID]models.User }

func (p *protectedUsers) GetByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	u, ok := p.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &u, nil
}

type profileStub struct{ user models.User }

func (p profileStub) GetMyProfile(context.Context, uuid.UUID) (models.User, error) {
	return p.user, nil
}

func (p profileStub) UpdateMyProfile(context.Context, uuid.UUID, service.ProfileInput) (models.User, error) {
	return p.user, nil
}

type adminStub struct{ user models.User }

func (a adminStub) GetUserByEmail(context.Context, uuid.UUID, string) (models.User, error) {
	return a.user, nil
}

func (a adminStub) ChangeUserRole(context.Context, uuid.UUID, uuid.UUID, models.RoleName, string) (models.User, error) {
	return a.user, nil
}

func TestProtectedConnectRoutes(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := jwt.NewES256Codec(key, "test-key", authmiddleware.TokenIssuer, authmiddleware.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	actorID, targetID := uuid.New(), uuid.New()
	actor := models.User{ID: actorID, Email: "actor@example.com", DisplayName: "Actor", Role: models.RoleUser}
	contact := "private_handle"
	target := models.User{ID: targetID, Email: "target@example.com", DisplayName: "Target",
		TelegramHandle: &contact, Role: models.RoleUser}
	users := &protectedUsers{users: map[uuid.UUID]models.User{actorID: actor, targetID: target}}
	handler := router.Setup(testHealth(), &authhandler.Handler{Logic: &stubLogic{}, AllowedOrigin: frontendOrigin},
		router.ProtectedRoutes{
			Profile:             &profilehandler.Handler{Logic: profileStub{actor}},
			Admin:               &adminhandler.Handler{Logic: adminStub{target}},
			Authenticate:        authmiddleware.AuthenticateLocal(codec),
			Users:               users,
			ProfileReadPolicy:   authorization.NewRolePolicy(authorization.RoleUser, authorization.RoleAdmin, authorization.RoleSuperAdmin, authorization.RoleSuspended),
			ProfileUpdatePolicy: authorization.NewRolePolicy(authorization.RoleUser, authorization.RoleAdmin, authorization.RoleSuperAdmin),
			AdminPolicy:         authorization.NewRolePolicy(authorization.RoleAdmin, authorization.RoleSuperAdmin),
		})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	profileClient := userv1connect.NewProfileServiceClient(server.Client(), server.URL)
	adminClient := userv1connect.NewUserAdminServiceClient(server.Client(), server.URL)
	ctx := context.Background()
	if _, err := profileClient.GetMyProfile(ctx, connect.NewRequest(&userv1.GetMyProfileRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing bearer token: %v", err)
	}
	token := signedTestAccess(t, codec, actorID, jwt.RoleUser)
	get := connect.NewRequest(&userv1.GetMyProfileRequest{})
	get.Header().Set("Authorization", "Bearer "+token)
	response, err := profileClient.GetMyProfile(ctx, get)
	if err != nil || response.Msg.Profile.Id != actorID.String() || response.Msg.Profile.Email != actor.Email {
		t.Fatalf("own profile: %+v, %v", response, err)
	}
	lookup := connect.NewRequest(&userv1.GetUserByEmailRequest{Email: target.Email})
	lookup.Header().Set("Authorization", "Bearer "+token)
	if _, err := adminClient.GetUserByEmail(ctx, lookup); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ordinary user lookup: %v", err)
	}
	profileUpdate := connect.NewRequest(&userv1.UpdateMyProfileRequest{
		DisplayName:    " " + strings.Repeat("N", 100) + " ",
		TelegramHandle: stringPtr(" private_handle "), PhoneNumber: stringPtr(" +123 "),
	})
	profileUpdate.Header().Set("Authorization", "Bearer "+token)
	if _, err := profileClient.UpdateMyProfile(ctx, profileUpdate); err != nil {
		t.Fatalf("normalized profile input rejected by RPC validation: %v", err)
	}
	actor.Role = models.RoleSuspended
	users.users[actorID] = actor
	if _, err := profileClient.GetMyProfile(ctx, get); err != nil {
		t.Fatalf("suspended profile read: %v", err)
	}
	update := connect.NewRequest(&userv1.UpdateMyProfileRequest{DisplayName: "New"})
	update.Header().Set("Authorization", "Bearer "+token)
	if _, err := profileClient.UpdateMyProfile(ctx, update); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended update with stale user token: %v", err)
	}
	invalidUpdate := connect.NewRequest(&userv1.UpdateMyProfileRequest{})
	invalidUpdate.Header().Set("Authorization", "Bearer "+token)
	if _, err := profileClient.UpdateMyProfile(ctx, invalidUpdate); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("authorization should precede validation: %v", err)
	}
	actor.Role = models.RoleAdmin
	users.users[actorID] = actor
	lookup = connect.NewRequest(&userv1.GetUserByEmailRequest{Email: target.Email})
	lookup.Header().Set("Authorization", "Bearer "+token)
	if response, err := adminClient.GetUserByEmail(ctx, lookup); err != nil || response.Msg.User.Id != targetID.String() {
		t.Fatalf("persisted admin lookup: %+v, %v", response, err)
	}
	actor.Role = models.RoleUser
	users.users[actorID] = actor
	staleAdminToken := signedTestAccess(t, codec, actorID, jwt.RoleAdmin)
	change := connect.NewRequest(&userv1.ChangeUserRoleRequest{UserId: targetID.String(), ToRole: userv1.UserRole_USER_ROLE_ADMIN})
	change.Header().Set("Authorization", "Bearer "+staleAdminToken)
	if _, err := adminClient.ChangeUserRole(ctx, change); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("stale admin token: %v", err)
	}
}

func signedTestAccess(t *testing.T, codec *jwt.ES256Codec, subject uuid.UUID, role jwt.Role) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	token, err := codec.Sign(jwt.Claims{Type: jwt.AccessToken, Subject: subject.String(),
		SessionID: "00000000-0000-4000-8000-000000000001", Role: role, IssuedAt: now,
		ExpiresAt: now.Add(time.Minute), TokenID: "00000000-0000-4000-8000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}
